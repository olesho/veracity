import { describe, it, expect } from "vitest";
import { EnglishGreeter } from "../src/greeter";

describe("EnglishGreeter", () => {
  it("defaults empty name to World", () => {
    expect(new EnglishGreeter().greet("")).toBe("Hello, World!");
  });
  it("greets a name", () => {
    expect(new EnglishGreeter().greet("Ada")).toBe("Hello, Ada!");
  });
});
