/** Greeter produces a greeting for a name — the module's boundary. */
export interface Greeter {
  greet(name: string): string;
}

/** EnglishGreeter greets in English. */
export class EnglishGreeter implements Greeter {
  greet(name: string): string {
    return `Hello, ${name || "World"}!`;
  }
}
