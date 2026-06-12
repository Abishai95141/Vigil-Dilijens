import { describe, expect, it } from "vitest";
import { entityLabel, shortMetric } from "./types";

describe("entityLabel", () => {
  it("renders an instance CEI as ns/name (kind)", () => {
    expect(entityLabel("i|cl|shop|Pod|web-a|uid-a")).toBe("shop/web-a (Pod)");
  });
  it("drops the namespace for cluster-scoped kinds", () => {
    expect(entityLabel("i|cl||Node|worker-1|uid-n")).toBe("worker-1 (Node)");
  });
  it("passes non-CEI strings through unchanged", () => {
    expect(entityLabel("not-a-cei")).toBe("not-a-cei");
  });
});

describe("shortMetric", () => {
  it("trims common prefixes and suffixes", () => {
    expect(shortMetric("container_memory_working_set_bytes")).toBe(
      "memory_working_set",
    );
    expect(shortMetric("node_pressure_cpu_waiting_seconds_total")).toBe(
      "pressure_cpu_waiting_seconds",
    );
  });
});
