package jobs

import (
	"time"

	"github.com/riverqueue/river"
)

const (
	QueueHello     = "hello"
	QueueScheduler = "scheduler"
	QueueAgent     = "agent"

	KindHelloJob      = "hello_job"
	KindScheduleHello = "schedule_hello"
	KindAgentRun      = "agent_run"
)

type HelloArgs struct {
	Message     string `json:"message"`
	RunKey      string `json:"run_key" river:"unique"`
	ScheduledBy string `json:"scheduled_by"`
}

func (HelloArgs) Kind() string { return KindHelloJob }

func (HelloArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       QueueHello,
		MaxAttempts: 3,
	}
}

type ScheduleHelloArgs struct {
	RunKey string `json:"run_key" river:"unique"`
}

func (ScheduleHelloArgs) Kind() string { return KindScheduleHello }

func (ScheduleHelloArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       QueueScheduler,
		MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: time.Minute,
		},
	}
}

type AgentRunArgs struct {
	TurnID string `json:"turn_id" river:"unique"`
}

func (AgentRunArgs) Kind() string { return KindAgentRun }

func (AgentRunArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       QueueAgent,
		MaxAttempts: 1,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
		},
	}
}

