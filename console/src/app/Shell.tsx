import { useConfig } from "@/api/client";
import { Icon } from "@/components/ui/icons";
import { Mono } from "@/components/ui/primitives";
import { Link, Outlet, useRouterState } from "@tanstack/react-router";
import { NAV, NavIcon } from "./nav";

function GateBanner() {
  const { data } = useConfig();
  const fc = data?.forecast;
  return (
    <header className="flex h-12 shrink-0 items-center gap-4 border-b border-rule bg-plane/80 px-5 backdrop-blur">
      <div className="flex items-center gap-2 text-ink-mid">
        <Icon.cluster size={15} />
        <span className="text-[12.5px] font-medium text-ink">
          {data?.clusterId ? data.clusterId.slice(0, 8) : "cluster"}
        </span>
        <span className="text-ink-mute">·</span>
        <Mono className="text-[11px] text-ink-low">
          {data?.graphRelease || data?.graphVersion?.replace("sha256:", "").slice(0, 10) || "graph"}
        </Mono>
      </div>

      <div className="ml-auto flex items-center gap-3">
        <span
          className="inline-flex items-center gap-1.5 rounded-[5px] border px-2 py-1 font-mono text-[10px] tracking-[0.06em]"
          style={{
            borderColor: fc?.enabled ? "var(--color-info-edge)" : "var(--color-rule-strong)",
            color: fc?.enabled ? "var(--color-info)" : "var(--color-ink-low)",
            background: fc?.enabled ? "var(--color-info-tint)" : "transparent",
          }}
          title={fc?.gateNote || "forecast lane"}
        >
          <Icon.forecast size={12} />
          forecast {fc?.enabled ? "on" : "gated"}
        </span>
        <span
          className="inline-flex items-center gap-1.5 text-[11px] text-ink-low"
          title="live, polling every 15s"
        >
          <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-success" />
          live
        </span>
      </div>
    </header>
  );
}

function SidebarLink({
  to,
  label,
  icon,
}: { to: string; label: string; icon: Parameters<typeof NavIcon>[0]["name"] }) {
  const active = useRouterState({
    select: (s) => {
      const p = s.location.pathname;
      return to === "/" ? p === "/" : p === to || p.startsWith(`${to}/`);
    },
  });
  return (
    <Link
      to={to}
      className={`group flex items-center gap-2.5 rounded-[6px] px-2.5 py-[7px] text-[12.5px] transition-colors duration-150 ${
        active ? "bg-surface-hi text-ink" : "text-ink-mid hover:bg-surface hover:text-ink"
      }`}
    >
      <span className={active ? "text-ink" : "text-ink-low group-hover:text-ink-mid"}>
        <NavIcon name={icon} size={16} />
      </span>
      {label}
      {active && <span className="ml-auto h-1 w-1 rounded-full bg-signal" />}
    </Link>
  );
}

export function Shell() {
  return (
    <div className="flex h-dvh w-full overflow-hidden bg-plane text-ink">
      {/* sidebar */}
      <nav className="flex w-[216px] shrink-0 flex-col border-r border-rule bg-plane">
        <Link to="/" className="flex h-12 items-center gap-2.5 border-b border-rule px-5">
          <span className="text-ink">
            <Icon.logo size={20} />
          </span>
          <span className="font-display text-[16px] font-semibold tracking-tight text-ink">
            Vigil
          </span>
          <span className="v-eyebrow ml-auto">v0.1</span>
        </Link>

        <div className="flex-1 overflow-y-auto px-3 py-3">
          {NAV.map((group, gi) => (
            <div key={group.label || `g${gi}`} className={gi > 0 ? "mt-5" : ""}>
              {group.label && <div className="v-eyebrow mb-1.5 px-2.5">{group.label}</div>}
              <div className="flex flex-col gap-0.5">
                {group.items.map((it) => (
                  <SidebarLink key={it.to} to={it.to} label={it.label} icon={it.icon} />
                ))}
              </div>
            </div>
          ))}
        </div>
      </nav>

      {/* main column */}
      <div className="flex min-w-0 flex-1 flex-col">
        <GateBanner />
        <main className="min-h-0 flex-1 overflow-hidden">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
