export interface Pair {
  a: number;
  b: number;
}

export const add = ({ a, b }: Pair): number => a + b;

export default function greet(name: string): string {
  return `hi ${name}`;
}
