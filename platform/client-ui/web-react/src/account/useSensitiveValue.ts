import { useCallback, useState } from "react";

export interface SensitiveValue {
  readonly value: string;
  readonly setValue: (value: string) => void;
  readonly clear: () => void;
}

export function useSensitiveValue(): SensitiveValue {
  const [value, setValue] = useState("");
  const clear = useCallback(() => setValue(""), []);
  return { value, setValue, clear };
}
