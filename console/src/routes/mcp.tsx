// MCP / Integrations (TRUTH) — POST /mcp. Connect a read-only AI to Vigil's
// harness. Live connection status, copy-pasteable client configs, the 7-tool
// catalog with provenance chips, a real test round-trip, and the DELIBERATE gaps:
// root-cause-chain, cross-service, departures, and topology are NOT MCP tools — so
// a client can never be handed raw edges to author causation. A feature, surfaced.

import { mcpCall, mcpInitialize, mcpListTools } from "@/api/client";
import type { McpInitializeResult, McpToolDef, McpToolName } from "@/api/types";
import { Icon } from "@/components/ui/icons";
import { LaneNote, ProvChip, SectionHead, Spinner, StateDot } from "@/components/ui/primitives";
import { CodeBlock, Page, Tag } from "@/components/ui/widgets";
import { useEffect, useState } from "react";

const PROV: Record<McpToolName, "MEASURED" | "PROJECTED" | "ADVISORY"> = {
  get_coverage: "MEASURED",
  get_silence_ledger: "MEASURED",
  get_incidents: "MEASURED",
  get_events: "MEASURED",
  get_warnings: "PROJECTED",
  validate_claim: "ADVISORY",
  emit_advisory: "ADVISORY",
};

function ToolProv({ name }: { name: McpToolName }) {
  const p = PROV[name];
  return p === "ADVISORY" ? <Tag sev="info">ADVISORY</Tag> : <ProvChip kind={p} />;
}

export function McpPage() {
  const [host, setHost] = useState("127.0.0.1");
  const [init, setInit] = useState<McpInitializeResult | null>(null);
  const [tools, setTools] = useState<McpToolDef[] | null>(null);
  const [status, setStatus] = useState<"idle" | "ok" | "err">("idle");
  const [testLog, setTestLog] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    Promise.all([mcpInitialize(), mcpListTools()])
      .then(([i, t]) => {
        if (!live) return;
        setInit(i);
        setTools(t.tools);
        setStatus("ok");
      })
      .catch(() => live && setStatus("err"));
    return () => {
      live = false;
    };
  }, []);

  async function test() {
    setBusy(true);
    setTestLog(null);
    const t0 = performance.now();
    try {
      const i = await mcpInitialize();
      const t = await mcpListTools();
      const ledger = await mcpCall("get_silence_ledger");
      const ms = Math.round(performance.now() - t0);
      const off =
        "available" in (ledger as object) &&
        (ledger as { available?: boolean }).available === false;
      setTestLog(
        [
          `initialize → ${i.serverInfo.name} (proto ${i.protocolVersion})`,
          `tools/list → ${t.tools.length} tools`,
          `tools/call get_silence_ledger → ${off ? "lane OFF (honest, not an error)" : "ok, ledger returned"}`,
          `round-trip ${ms} ms · PASS`,
        ].join("\n"),
      );
    } catch (e) {
      setTestLog(`FAIL — ${String(e)}`);
    } finally {
      setBusy(false);
    }
  }

  const claudeDesktop = JSON.stringify(
    {
      mcpServers: {
        vigil: {
          command: "npx",
          args: ["-y", "mcp-remote", `http://${host}:9095/mcp`, "--transport", "http-only"],
        },
      },
    },
    null,
    2,
  );
  const claudeCode = JSON.stringify(
    { mcpServers: { vigil: { type: "http", url: `http://${host}:9095/mcp` } } },
    null,
    2,
  );

  return (
    <Page>
      <SectionHead
        num="14"
        title="MCP / Integrations"
        lede="Connect a read-only AI to Vigil. It can read coverage, ask the silence ledger what is not watched and why, pull forecast bands, events, and recurrences, and run its own prose through the referee — but it can never be handed raw edges to author a cause."
      />

      <div className="flex flex-col gap-6">
        {/* connection status */}
        <div className="v-panel p-4">
          <div className="flex flex-wrap items-center gap-3">
            <StateDot
              sev={status === "ok" ? "ok" : status === "err" ? "firing" : "neutral"}
              live={status === "ok"}
            />
            <span className="text-[13px] font-medium text-ink">
              {status === "ok"
                ? `Connected · ${init?.serverInfo.name}`
                : status === "err"
                  ? "Unreachable"
                  : "Connecting…"}
            </span>
            {init && (
              <span className="font-mono text-[11px] text-ink-low">
                proto {init.protocolVersion} · version {init.serverInfo.version || "(dev)"}
              </span>
            )}
            <button
              type="button"
              onClick={test}
              disabled={busy}
              className="ml-auto inline-flex cursor-pointer items-center gap-1.5 rounded-[6px] bg-signal px-3 py-1.5 text-[12px] font-medium text-plane transition-colors hover:bg-ink disabled:opacity-40"
            >
              {busy ? <Spinner size={12} /> : <Icon.mcp size={14} />}
              Test connection
            </button>
          </div>
          {testLog && (
            <pre className="v-panel-inset mt-3 overflow-x-auto p-3 text-[11.5px] leading-relaxed text-ink-soft">
              {testLog}
            </pre>
          )}
        </div>

        {/* config */}
        <div>
          <div className="mb-2 flex items-center gap-2">
            <span className="v-eyebrow">Client config</span>
            <span className="text-[11px] text-ink-low">host</span>
            <input
              value={host}
              onChange={(e) => setHost(e.target.value)}
              className="w-44 rounded-[5px] border border-rule-strong bg-surface-hi px-2 py-0.5 font-mono text-[11px] text-ink focus:border-ink-low focus:outline-none"
            />
            <span className="font-mono text-[11px] text-ink-low">
              :9095/mcp · plain HTTP JSON-RPC
            </span>
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            <div>
              <div className="mb-1 text-[11.5px] text-ink-mid">
                Claude Desktop (via mcp-remote bridge)
              </div>
              <CodeBlock code={claudeDesktop} />
            </div>
            <div>
              <div className="mb-1 text-[11.5px] text-ink-mid">Claude Code (.mcp.json)</div>
              <CodeBlock code={claudeCode} />
            </div>
          </div>
        </div>

        {/* tool catalog */}
        <div>
          <div className="v-eyebrow mb-2">Tool catalog — lead with the provable-negative tool</div>
          {tools ? (
            <div className="flex flex-col gap-2">
              {[...tools]
                .sort((a, b) =>
                  a.name === "get_silence_ledger" ? -1 : b.name === "get_silence_ledger" ? 1 : 0,
                )
                .map((t) => (
                  <div key={t.name} className="v-panel p-3.5">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="v-mono text-[12.5px] text-ink">{t.name}</span>
                      <ToolProv name={t.name} />
                      {t.inputSchema.required?.length ? (
                        <Tag>arg: {t.inputSchema.required.join(", ")}</Tag>
                      ) : (
                        <Tag>no args</Tag>
                      )}
                    </div>
                    <div className="mt-1.5 text-[12px] leading-relaxed text-ink-mid">
                      {t.description}
                    </div>
                  </div>
                ))}
            </div>
          ) : (
            <LaneNote
              kind={status === "err" ? "off" : "info"}
              note={status === "err" ? "MCP endpoint unreachable." : "Loading tools…"}
            />
          )}
        </div>

        {/* deliberate gaps + auth */}
        <LaneNote
          kind="info"
          title="Deliberately not MCP tools"
          note="root-cause-chain, cross-service, departures, and topology are web-only — by design. An MCP client can relay classed facts, but is never handed the raw edges to author causation. Route any AI-drafted causal sentence through validate_claim first."
        />
        <div className="v-panel-inset flex items-start gap-3 p-4">
          <Icon.shield size={16} className="mt-0.5 shrink-0 text-warning" />
          <div className="text-[12px] leading-relaxed text-ink-mid">
            <span className="font-medium text-warning">No auth today.</span> Bind to localhost /
            cluster-internal only — never a public IP. Bearer-token / mTLS is pending the auth
            track.
          </div>
        </div>
      </div>
    </Page>
  );
}
