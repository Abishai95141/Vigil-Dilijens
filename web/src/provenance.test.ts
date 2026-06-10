import { expect, test } from "vitest";
import { PROVENANCE, type ProvenanceClass, styleFor } from "./provenance";

test("every provenance class has a distinct register and token", () => {
  const classes: ProvenanceClass[] = ["MEASURED", "PROJECTED", "AUTHORED"];
  const registers = new Set(classes.map((c) => PROVENANCE[c].register));
  const tokens = new Set(classes.map((c) => PROVENANCE[c].token));
  // Distinct registers and tokens enforce the "never fuse on screen" rule (doc 10 §3.1).
  expect(registers.size).toBe(3);
  expect(tokens.size).toBe(3);
});

test("PROJECTED is always modal (doc 01 §5)", () => {
  expect(styleFor("PROJECTED").register).toBe("modal");
});
