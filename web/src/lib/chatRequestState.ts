export type ChatRequestState = {
  input: string;
  loading: boolean;
};

export function nextChatStateAfterCreateError(
  current: ChatRequestState,
  submittedText: string,
  runCreated: boolean,
): ChatRequestState {
  if (runCreated) {
    return {
      ...current,
      loading: false,
    };
  }
  return {
    ...current,
    input: submittedText,
    loading: false,
  };
}
