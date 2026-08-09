declare module 'bun:test' {
  export const describe: (name: string, test: () => void) => void;
  export const test: (name: string, test: () => void | Promise<void>) => void;
  export const expect: (value: unknown) => {
    toBe(expected: unknown): void;
    toContain(expected: unknown): void;
    toEqual(expected: unknown): void;
    toThrow(): void;
  };
}
