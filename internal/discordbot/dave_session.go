package discordbot

import (
	"log/slog"

	"github.com/disgoorg/godave"
	"github.com/disgoorg/godave/libdave"
)

const (
	daveInitTransitionID         = 0
	daveDisabledProtocolVersion  = 0
	daveMLSNewGroupExpectedEpoch = 1
)

// newDaveSession builds a DAVE session on top of libdave primitives. The
// structure mirrors github.com/disgoorg/godave/golibdave v0.1.0 but fixes the
// bugs that broke receive-side audio for us:
//
//  1. The passthrough branch of Decrypt copies the encrypted frame into the
//     output buffer. golibdave had the arguments swapped, zeroing the
//     output and dropping every packet that fell through to passthrough
//     (SSRCs Discord hasn't announced via a Speaking opcode yet).
//     `godave.NoopSession.Decrypt` is the reference for the correct
//     direction: `copy(decryptedFrame, frame)`.
//
//  2. setupKeyRatchetForUser refuses to flip a decryptor/encryptor out of
//     passthrough mode when the underlying MLS session has no ratchet for
//     that user yet. `libdave.NewDecryptor` starts in non-passthrough mode
//     with no cryptor, so a user registered before the welcome/commit for
//     them lands on our side would otherwise sit in a "non-passthrough, no
//     cryptor" state — libdave C++ logs that as "no valid cryptor found,
//     pass through enabled: no" and drops every frame.
//
// The transition lifecycle — eager for remote decryptors in
// prepareTransition, deferred for the self encryptor via preparedTransitions,
// with init transitions (id == 0) applied immediately because Discord does
// not follow them with an ExecuteTransition — matches golibdave, which
// Discord's MLS flow relies on.
func newDaveSession(logger *slog.Logger, selfUserID godave.UserID, callbacks godave.Callbacks) godave.Session {
	if logger == nil {
		logger = slog.Default()
	}
	encryptor := libdave.NewEncryptor()
	encryptor.SetPassthroughMode(true)

	return &daveSession{
		selfUserID:          selfUserID,
		logger:              logger,
		callbacks:           callbacks,
		session:             libdave.NewSession("", ""),
		encryptor:           encryptor,
		decryptors:          make(map[godave.UserID]*libdave.Decryptor),
		preparedTransitions: make(map[uint16]uint16),
	}
}

type daveSession struct {
	selfUserID                    godave.UserID
	channelID                     godave.ChannelID
	logger                        *slog.Logger
	callbacks                     godave.Callbacks
	session                       *libdave.Session
	encryptor                     *libdave.Encryptor
	decryptors                    map[godave.UserID]*libdave.Decryptor
	preparedTransitions           map[uint16]uint16
	lastPreparedTransitionVersion uint16
}

func (s *daveSession) MaxSupportedProtocolVersion() int {
	return int(libdave.MaxSupportedProtocolVersion())
}

func (s *daveSession) SetChannelID(channelID godave.ChannelID) {
	s.channelID = channelID
}

func (s *daveSession) AssignSsrcToCodec(ssrc uint32, codec godave.Codec) {
	s.encryptor.AssignSsrcToCodec(ssrc, libdave.Codec(codec))
}

func (s *daveSession) MaxEncryptedFrameSize(frameSize int) int {
	return s.encryptor.GetMaxCiphertextByteSize(libdave.MediaTypeAudio, frameSize)
}

func (s *daveSession) Encrypt(ssrc uint32, frame []byte, encryptedFrame []byte) (int, error) {
	return s.encryptor.Encrypt(libdave.MediaTypeAudio, ssrc, frame, encryptedFrame)
}

func (s *daveSession) MaxDecryptedFrameSize(userID godave.UserID, frameSize int) int {
	if decryptor, ok := s.decryptors[userID]; ok {
		return decryptor.GetMaxPlaintextByteSize(libdave.MediaTypeAudio, frameSize)
	}
	return frameSize
}

func (s *daveSession) Decrypt(userID godave.UserID, frame []byte, decryptedFrame []byte) (int, error) {
	if decryptor, ok := s.decryptors[userID]; ok {
		n, err := decryptor.Decrypt(libdave.MediaTypeAudio, frame, decryptedFrame)
		if err != nil {
			s.logger.Debug("dave decrypt failure",
				slog.String("user_id", string(userID)),
				slog.Int("frame_len", len(frame)),
				slog.Any("err", err),
			)
		}
		return n, err
	}
	// No decryptor for this userID. This is the unknown-SSRC path: disgo
	// returns a zero snowflake from `UserIDBySSRC` until a
	// GatewayMessageDataSpeaking has mapped the SSRC. When DAVE is active
	// those frames are ciphertext, and blindly copying them through would
	// feed encrypted bytes to the Opus decoder (the "opus: corrupted stream"
	// storm we used to see). Drop them with a zero-length success so disgo
	// keeps the read loop going and our receiver skips the empty frame.
	if s.lastPreparedTransitionVersion != daveDisabledProtocolVersion {
		return 0, nil
	}
	return copy(decryptedFrame, frame), nil
}

func (s *daveSession) AddUser(userID godave.UserID) {
	if _, ok := s.decryptors[userID]; ok {
		return
	}
	decryptor := libdave.NewDecryptor()
	// Override libdave's default non-passthrough-no-cryptor state. Until a
	// prepareTransition or the next MLS commit installs a real ratchet, any
	// packets that arrive for this user should pass through rather than
	// fail with "no valid cryptor found".
	decryptor.TransitionToPassthroughMode(true)
	s.decryptors[userID] = decryptor
	s.setupKeyRatchetForUser(userID, s.lastPreparedTransitionVersion)
}

func (s *daveSession) RemoveUser(userID godave.UserID) {
	delete(s.decryptors, userID)
}

func (s *daveSession) OnSelectProtocolAck(protocolVersion uint16) {
	s.protocolInit(protocolVersion)
}

func (s *daveSession) OnDavePrepareTransition(transitionID uint16, protocolVersion uint16) {
	s.prepareTransition(transitionID, protocolVersion)
	if transitionID != daveInitTransitionID {
		s.sendReadyForTransition(transitionID)
	}
}

func (s *daveSession) OnDaveExecuteTransition(transitionID uint16) {
	s.executeTransition(transitionID)
}

func (s *daveSession) OnDavePrepareEpoch(epoch int, protocolVersion uint16) {
	s.prepareEpoch(epoch, protocolVersion)
	if epoch == daveMLSNewGroupExpectedEpoch {
		s.sendMLSKeyPackage()
	}
}

func (s *daveSession) OnDaveMLSExternalSenderPackage(externalSenderPackage []byte) {
	s.session.SetExternalSender(externalSenderPackage)
}

func (s *daveSession) OnDaveMLSProposals(proposals []byte) {
	commitWelcome := s.session.ProcessProposals(proposals, s.recognizedUserIDs())
	if commitWelcome != nil {
		s.sendMLSCommitWelcome(commitWelcome)
	}
}

func (s *daveSession) OnDaveMLSPrepareCommitTransition(transitionID uint16, commitMessage []byte) {
	res := s.session.ProcessCommit(commitMessage)
	if res.IsIgnored() {
		return
	}
	if res.IsFailed() {
		s.sendInvalidCommitWelcome(transitionID)
		s.protocolInit(s.session.GetProtocolVersion())
		return
	}
	s.prepareTransition(transitionID, s.session.GetProtocolVersion())
	if transitionID != daveInitTransitionID {
		s.sendReadyForTransition(transitionID)
	}
}

func (s *daveSession) OnDaveMLSWelcome(transitionID uint16, welcomeMessage []byte) {
	res := s.session.ProcessWelcome(welcomeMessage, s.recognizedUserIDs())
	if res == nil {
		s.sendInvalidCommitWelcome(transitionID)
		s.sendMLSKeyPackage()
		return
	}
	s.prepareTransition(transitionID, s.session.GetProtocolVersion())
	if transitionID != daveInitTransitionID {
		s.sendReadyForTransition(transitionID)
	}
}

func (s *daveSession) recognizedUserIDs() []string {
	userIDs := make([]string, 0, len(s.decryptors)+1)
	userIDs = append(userIDs, string(s.selfUserID))
	for userID := range s.decryptors {
		userIDs = append(userIDs, string(userID))
	}
	return userIDs
}

func (s *daveSession) protocolInit(protocolVersion uint16) {
	if protocolVersion > daveDisabledProtocolVersion {
		s.prepareEpoch(daveMLSNewGroupExpectedEpoch, protocolVersion)
		s.sendMLSKeyPackage()
	} else {
		s.prepareTransition(daveInitTransitionID, protocolVersion)
	}
}

func (s *daveSession) prepareEpoch(epoch int, protocolVersion uint16) {
	if epoch != daveMLSNewGroupExpectedEpoch {
		return
	}
	s.session.Init(protocolVersion, uint64(s.channelID), string(s.selfUserID))
}

// prepareTransition installs the new ratchets for every remote decryptor
// eagerly. The self encryptor is deferred to executeTransition so we keep
// sending in the old mode until Discord confirms every remote is ready —
// except when transitionID == 0, the init transition that has no matching
// ExecuteTransition on the wire, in which case we apply self immediately.
func (s *daveSession) prepareTransition(transitionID uint16, protocolVersion uint16) {
	s.lastPreparedTransitionVersion = protocolVersion
	for userID := range s.decryptors {
		s.setupKeyRatchetForUser(userID, protocolVersion)
	}
	if transitionID == daveInitTransitionID {
		s.setupKeyRatchetForUser(s.selfUserID, protocolVersion)
		return
	}
	s.preparedTransitions[transitionID] = protocolVersion
}

func (s *daveSession) executeTransition(transitionID uint16) {
	protocolVersion, ok := s.preparedTransitions[transitionID]
	if !ok {
		return
	}
	delete(s.preparedTransitions, transitionID)

	if protocolVersion == daveDisabledProtocolVersion {
		s.session.Reset()
	}
	s.setupKeyRatchetForUser(s.selfUserID, protocolVersion)
}

// setupKeyRatchetForUser is the only place decryptors/encryptor swap between
// passthrough and encrypted modes. It guards against flipping out of
// passthrough when the MLS session has no ratchet available for `userID`:
// libdave's C++ side returns an empty KeyRatchet handle in that case, and
// calling TransitionToKeyRatchet on it would leave the cryptor in a
// non-passthrough-no-cryptor state (the "no valid cryptor found" failure).
// Without a ratchet we stay in passthrough; a subsequent
// prepareTransition/executeTransition pair will upgrade us once the MLS
// commit for that user has been processed.
func (s *daveSession) setupKeyRatchetForUser(userID godave.UserID, protocolVersion uint16) {
	disabled := protocolVersion == daveDisabledProtocolVersion

	if userID == s.selfUserID {
		if disabled {
			s.encryptor.SetPassthroughMode(true)
			return
		}
		ratchet := s.session.GetKeyRatchet(string(userID))
		if ratchet == nil {
			s.encryptor.SetPassthroughMode(true)
			return
		}
		s.encryptor.SetPassthroughMode(false)
		s.encryptor.SetKeyRatchet(ratchet)
		return
	}

	decryptor, ok := s.decryptors[userID]
	if !ok {
		return
	}
	if disabled {
		decryptor.TransitionToPassthroughMode(true)
		return
	}
	ratchet := s.session.GetKeyRatchet(string(userID))
	if ratchet == nil {
		decryptor.TransitionToPassthroughMode(true)
		return
	}
	decryptor.TransitionToPassthroughMode(false)
	decryptor.TransitionToKeyRatchet(ratchet)
}

func (s *daveSession) sendMLSKeyPackage() {
	if err := s.callbacks.SendMLSKeyPackage(s.session.GetMarshalledKeyPackage()); err != nil {
		s.logger.Error("failed to send MLS key package", slog.Any("err", err))
	}
}

func (s *daveSession) sendMLSCommitWelcome(message []byte) {
	if err := s.callbacks.SendMLSCommitWelcome(message); err != nil {
		s.logger.Error("failed to send MLS commit welcome", slog.Any("err", err))
	}
}

func (s *daveSession) sendReadyForTransition(transitionID uint16) {
	if err := s.callbacks.SendReadyForTransition(transitionID); err != nil {
		s.logger.Error("failed to send ready for transition", slog.Any("err", err))
	}
}

func (s *daveSession) sendInvalidCommitWelcome(transitionID uint16) {
	if err := s.callbacks.SendInvalidCommitWelcome(transitionID); err != nil {
		s.logger.Error("failed to send invalid commit welcome", slog.Any("err", err))
	}
}
