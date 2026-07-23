// Veracity TypeScript extractor (esbuild bundle, checked in).
//
// Reads a JSON request on stdin: {subproject, root, files:[abs paths]} and
// writes a JSON IR module list on stdout, using the TypeScript Compiler API.
//
// NOTE: filled in by the docgen task; currently emits an empty module list.
// The source lives in extractors-src/ts and is rebuilt with esbuild (CI diffs
// the rebuild against this file).
let input = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => (input += chunk));
process.stdin.on("end", () => {
  try {
    JSON.parse(input || "{}");
  } catch (err) {
    process.stderr.write(`extract_ts: invalid request: ${err}\n`);
    process.exit(1);
  }
  process.stdout.write(JSON.stringify({ modules: [] }));
});
