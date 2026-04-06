type UserMessageProps = {
  text: string;
};

export function UserMessage({ text }: UserMessageProps) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[85%] rounded-none bg-white/12 px-5 py-4 text-sm text-white shadow-lg">
        <p className="whitespace-pre-wrap leading-relaxed">{text}</p>
      </div>
    </div>
  );
}
