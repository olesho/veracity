import js from "@eslint/js";
import tseslint from "typescript-eslint";

// Type-aware strict linting: strictTypeChecked catches type-driven bugs (unsafe
// any, floating promises, unnecessary conditions) that the non-type-aware rules
// miss. It requires the TypeScript program, enabled below via projectService.
export default tseslint.config(
  // Build output, deps, coverage, and tooling config files (the config files are
  // not part of the tsconfig program, so type-aware rules can't resolve them).
  {
    ignores: [
      "dist/",
      "node_modules/",
      "coverage/",
      "**/*.config.{js,ts,mjs,cjs,mts,cts}",
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.strictTypeChecked,
  ...tseslint.configs.stylisticTypeChecked,
  {
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
);
