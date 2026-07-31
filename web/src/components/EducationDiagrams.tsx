import type { ReactNode } from "react";

/**
 * Inline SVG figures for the education center.
 *
 * No charting dependency on purpose: the console is embedded in the gateway
 * binary, ships to air-gapped deployments and must make no outbound request, so
 * a library would add weight to a 1.5 MB bundle for five static figures.
 *
 * Colour is assigned by the job it does, not by taste. Two categorical hues
 * carry one meaning consistently across every figure on the page:
 *
 *   OUTBOUND — the submit side: MT traffic, submit_sm_resp, money taken at submit
 *   INBOUND  — the return side: MO traffic, carrier receipts, money settled later
 *
 * The console's brand teal (#0f766e) is deliberately NOT used as a mark colour:
 * at chroma 0.086 it falls below the floor where a fill still reads as a colour
 * rather than as gray. These two steps were validated together — lightness band,
 * chroma floor, colour-vision separation (ΔE 21.2 deutan / 14.2 tritan), and
 * contrast against a white card — and every figure also carries a direct label,
 * so identity never rests on colour alone.
 */
const OUTBOUND = "#12a594";
const INBOUND = "#5b5bd6";
const INK = "#17201e";
const MUTED = "#62706c";
const LINE = "#dfe6e2";
const SURFACE = "#ffffff";

type FigureProps = {
  title: string;
  description: string;
  caption?: ReactNode;
  children: ReactNode;
  viewBox: string;
};

/**
 * Figure wraps one diagram with its accessible name and description. Screen
 * readers get the description; sighted readers get the caption, which carries
 * the point the picture is making rather than repeating what it shows.
 */
const Figure = ({ title, description, caption, children, viewBox }: FigureProps) => (
  <figure className="education-figure">
    <svg
      viewBox={viewBox}
      role="img"
      aria-label={title}
      preserveAspectRatio="xMidYMid meet"
      style={{ width: "100%", height: "auto", display: "block" }}
    >
      <title>{title}</title>
      <desc>{description}</desc>
      {children}
    </svg>
    {caption ? <figcaption>{caption}</figcaption> : null}
  </figure>
);

const Legend = ({ items }: { items: Array<{ color: string; label: string }> }) => (
  <ul className="education-figure-legend">
    {items.map((item) => (
      <li key={item.label}>
        <span style={{ background: item.color }} aria-hidden="true" />
        {item.label}
      </li>
    ))}
  </ul>
);

/** An endpoint box: the two parties in an SMPP session. */
const Endpoint = ({ x, y, label, sub }: { x: number; y: number; label: string; sub: string }) => (
  <g>
    <rect x={x} y={y} width={132} height={58} rx={10} fill={SURFACE} stroke={LINE} strokeWidth={2} />
    <text x={x + 66} y={y + 26} textAnchor="middle" fontSize={15} fontWeight={600} fill={INK}>
      {label}
    </text>
    <text x={x + 66} y={y + 44} textAnchor="middle" fontSize={12} fill={MUTED}>
      {sub}
    </text>
  </g>
);

/**
 * Node — the general-purpose labelled box used by the pipeline and structural
 * figures below (Endpoint, above, stays specific to the two-party SMPP
 * session). `accent` overrides the border colour; omit it for a neutral box.
 */
const Node = ({
  x,
  y,
  width,
  label,
  sub,
  accent,
}: {
  x: number;
  y: number;
  width: number;
  label: string;
  sub?: string;
  accent?: string;
}) => (
  <g>
    <rect x={x} y={y} width={width} height={60} rx={9} fill={SURFACE} stroke={accent ?? LINE} strokeWidth={2} />
    <text x={x + width / 2} y={y + (sub ? 25 : 34)} textAnchor="middle" fontSize={13} fontWeight={600} fill={INK}>
      {label}
    </text>
    {sub ? (
      <text x={x + width / 2} y={y + 43} textAnchor="middle" fontSize={10.5} fill={MUTED}>
        {sub}
      </text>
    ) : null}
  </g>
);

/**
 * SMPPSessionDiagram — the picture the "SMPP in one picture" lesson promises.
 * One session, two parties, and the three bind modes as directed lanes.
 */
export const SMPPSessionDiagram = () => (
  <Figure
    viewBox="0 0 640 250"
    title="SMPP bind modes between an ESME and an SMSC"
    description="An ESME on the left and an SMSC on the right. A transmitter bind carries submit_sm outbound only. A receiver bind carries deliver_sm inbound only. A transceiver bind carries both directions over one session."
    caption={
      <>
        <Legend
          items={[
            { color: OUTBOUND, label: "Outbound · submit_sm" },
            { color: INBOUND, label: "Inbound · deliver_sm" },
          ]}
        />
        The bind direction is about which side may <em>send PDUs</em>, not about who opened the TCP
        connection. The ESME always dials out, in all three modes.
      </>
    }
  >
    <defs>
      <marker id="arrow-out" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
        <path d="M0,0 L10,5 L0,10 z" fill={OUTBOUND} />
      </marker>
      <marker id="arrow-in" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
        <path d="M0,0 L10,5 L0,10 z" fill={INBOUND} />
      </marker>
    </defs>

    <Endpoint x={8} y={96} label="ESME" sub="your gateway" />
    <Endpoint x={500} y={96} label="SMSC" sub="carrier / aggregator" />

    {[
      { y: 42, mode: "Transmitter (TX)", lanes: ["out"] },
      { y: 125, mode: "Receiver (RX)", lanes: ["in"] },
      { y: 200, mode: "Transceiver (TRX)", lanes: ["out", "in"] },
    ].map((row) => (
      <g key={row.mode}>
        <text x={320} y={row.y - 14} textAnchor="middle" fontSize={12} fontWeight={600} fill={MUTED}>
          {row.mode}
        </text>
        {row.lanes.map((lane, index) => {
          const offset = row.lanes.length > 1 ? (index === 0 ? -8 : 8) : 0;
          const outbound = lane === "out";
          return (
            <line
              key={lane}
              x1={outbound ? 152 : 494}
              x2={outbound ? 494 : 152}
              y1={row.y + offset}
              y2={row.y + offset}
              stroke={outbound ? OUTBOUND : INBOUND}
              strokeWidth={2}
              markerEnd={outbound ? "url(#arrow-out)" : "url(#arrow-in)"}
            />
          );
        })}
      </g>
    ))}
  </Figure>
);

const SEGMENTS = [
  { label: "GSM-7, single part", value: 160 },
  { label: "GSM-7, concatenated", value: 153 },
  { label: "UCS-2, single part", value: 70 },
  { label: "UCS-2, concatenated", value: 67 },
];

/**
 * SegmentationChart — magnitude comparison, so bars; one series, so one hue and
 * no legend. Every bar is direct-labelled with its value, which is why there is
 * no tooltip: nothing is hidden behind an interaction.
 */
export const SegmentationChart = () => {
  const max = 160;
  const scale = 340;
  const rowHeight = 40;
  return (
    <Figure
      viewBox={`0 0 640 ${SEGMENTS.length * rowHeight + 34}`}
      title="Characters per SMS part by encoding"
      description="GSM-7 fits 160 characters in a single part and 153 per part once concatenated. UCS-2 fits 70 in a single part and 67 per part concatenated."
      caption={
        <>
          Concatenation costs capacity in every encoding: the header that links the parts together
          lives inside the message body. A 161-character GSM-7 message is therefore two parts of 153,
          not one of 160 plus one of 1 — and it is charged as two.
        </>
      }
    >
      {SEGMENTS.map((segment, index) => {
        const y = index * rowHeight + 8;
        const width = (segment.value / max) * scale;
        return (
          <g key={segment.label}>
            <text x={0} y={y + 20} fontSize={13} fill={INK}>
              {segment.label}
            </text>
            <rect x={216} y={y + 6} width={width} height={20} rx={4} fill={OUTBOUND} />
            <text x={216 + width + 10} y={y + 21} fontSize={13} fontWeight={600} fill={INK}>
              {segment.value}
            </text>
          </g>
        );
      })}
      <text x={216} y={SEGMENTS.length * rowHeight + 26} fontSize={12} fill={MUTED}>
        characters per part
      </text>
    </Figure>
  );
};

/**
 * ChargingSplitChart — a part-to-whole comparison of one rate, so a single
 * stacked bar per mode with a 2px surface gap between the segments.
 */
export const ChargingSplitChart = () => {
  const rows = [
    { mode: "Prepaid", early: 100, note: "the default: no split configured" },
    { mode: "Split 50%", early: 50, note: "early_decrement_balance_percent = 50" },
  ];
  const scale = 380;
  return (
    <Figure
      viewBox="0 0 640 172"
      title="How one message rate is split between submit time and acceptance"
      description="On a prepaid account the whole rate is taken when the message is admitted. On a split account only the configured percentage is taken at submit and the remainder is applied after the SMSC accepts the message."
      caption={
        <>
          <Legend
            items={[
              { color: OUTBOUND, label: "Taken at submit" },
              { color: INBOUND, label: "Applied after SMSC acceptance" },
            ]}
          />
          The early share is <strong>not refunded</strong> if the SMSC later rejects the message, and
          the late share is never applied to a rejected one. A pure pay-on-acceptance account cannot
          be configured here — the percentage is valid from 1 to 100.
        </>
      }
    >
      {rows.map((row, index) => {
        const y = index * 72 + 10;
        const earlyWidth = (row.early / 100) * scale;
        const lateWidth = scale - earlyWidth;
        return (
          <g key={row.mode}>
            <text x={0} y={y + 22} fontSize={14} fontWeight={600} fill={INK}>
              {row.mode}
            </text>
            <text x={0} y={y + 40} fontSize={12} fill={MUTED}>
              {row.note}
            </text>
            <rect x={216} y={y + 6} width={earlyWidth} height={26} rx={4} fill={OUTBOUND} />
            <text x={224} y={y + 24} fontSize={12} fontWeight={600} fill={SURFACE}>
              {row.early}%
            </text>
            {lateWidth > 0 ? (
              <>
                {/* 2px surface gap keeps the two shares from reading as one fill. */}
                <rect
                  x={216 + earlyWidth + 2}
                  y={y + 6}
                  width={lateWidth - 2}
                  height={26}
                  rx={4}
                  fill={INBOUND}
                />
                <text x={224 + earlyWidth} y={y + 24} fontSize={12} fontWeight={600} fill={SURFACE}>
                  {100 - row.early}%
                </text>
              </>
            ) : null}
          </g>
        );
      })}
    </Figure>
  );
};

/**
 * DLRLevelsDiagram — which callbacks each requested level produces. A schematic
 * rather than a chart: the horizontal axis is sequence, not a measured quantity.
 */
export const DLRLevelsDiagram = () => {
  const rows = [
    { level: "1", events: ["submit"], note: "submit answer only" },
    { level: "2", events: ["receipt"], note: "final state only" },
    { level: "3", events: ["submit", "receipt"], note: "both" },
  ];
  return (
    <Figure
      viewBox="0 0 640 218"
      title="Callbacks produced by each requested delivery-receipt level"
      description="Level 1 produces one callback when the SMSC answers the submit. Level 2 produces one callback later, when the carrier reports a final state, and none at all if the submit failed. Level 3 produces both."
      caption={
        <>
          <Legend
            items={[
              { color: OUTBOUND, label: "level=1 · submit answered" },
              { color: INBOUND, label: "level=2 · carrier receipt" },
            ]}
          />
          Level 2 is the trap: a failed submit never establishes correlation, so it produces{" "}
          <strong>no callback at all</strong> rather than a failure callback. Choose level 3 when you
          need to distinguish “rejected at submit” from “still waiting”.
        </>
      }
    >
      <line x1={140} y1={26} x2={140} y2={186} stroke={LINE} strokeWidth={2} />
      <text x={140} y={16} textAnchor="middle" fontSize={11} fill={MUTED}>
        submit
      </text>
      <text x={470} y={16} textAnchor="middle" fontSize={11} fill={MUTED}>
        minutes to hours later
      </text>

      {rows.map((row, index) => {
        const y = index * 56 + 44;
        return (
          <g key={row.level}>
            <text x={0} y={y + 5} fontSize={13} fontWeight={600} fill={INK}>
              Level {row.level}
            </text>
            <text x={0} y={y + 22} fontSize={11} fill={MUTED}>
              {row.note}
            </text>
            <line x1={140} y1={y} x2={600} y2={y} stroke={LINE} strokeWidth={2} strokeDasharray="4 6" />
            {row.events.includes("submit") ? (
              <>
                <circle cx={140} cy={y} r={7} fill={OUTBOUND} stroke={SURFACE} strokeWidth={2} />
                <text x={154} y={y + 4} fontSize={12} fill={INK}>
                  callback level=1
                </text>
              </>
            ) : null}
            {row.events.includes("receipt") ? (
              <>
                <circle cx={470} cy={y} r={7} fill={INBOUND} stroke={SURFACE} strokeWidth={2} />
                <text x={484} y={y + 4} fontSize={12} fill={INK}>
                  callback level=2
                </text>
              </>
            ) : null}
          </g>
        );
      })}
    </Figure>
  );
};

/**
 * RoutePriorityDiagram — why the first matching route wins, and why a broad
 * route above a specific one silently swallows its traffic.
 */
export const RoutePriorityDiagram = () => {
  const routes = [
    { order: "20", filter: "destination starts with 44", connector: "carrier-uk", matched: false },
    { order: "10", filter: "customer group = wholesale", connector: "carrier-bulk", matched: true },
    { order: "0", filter: "default · no filters", connector: "carrier-fallback", matched: false },
  ];
  return (
    <Figure
      viewBox="0 0 640 210"
      title="Route evaluation order and first-match selection"
      description="Routes are evaluated from the highest order down. The first route whose filters all match selects the connector, and evaluation stops there. The default route at order zero has no filters and catches everything that reached it."
      caption={
        <>
          Evaluation stops at the first match, so a broad route placed above a specific one takes its
          traffic and the specific route never fires. The order-0 default has no filters by
          definition — anything that reaches it matches.
        </>
      }
    >
      {routes.map((route, index) => {
        const y = index * 58 + 18;
        return (
          <g key={route.order}>
            <rect
              x={44}
              y={y}
              width={556}
              height={44}
              rx={8}
              fill={route.matched ? "#f0fbf8" : SURFACE}
              stroke={route.matched ? OUTBOUND : LINE}
              strokeWidth={2}
            />
            <text x={62} y={y + 27} fontSize={13} fontWeight={600} fill={INK}>
              order {route.order}
            </text>
            <text x={162} y={y + 27} fontSize={13} fill={MUTED}>
              {route.filter}
            </text>
            <text x={392} y={y + 27} fontSize={13} fill={INK}>
              → {route.connector}
            </text>
            {route.matched ? (
              <text x={588} y={y + 27} textAnchor="end" fontSize={11} fontWeight={600} fill={OUTBOUND}>
                MATCH · stop
              </text>
            ) : null}
            {index < routes.length - 1 && !route.matched ? (
              <path
                d={`M24,${y + 22} L24,${y + 58}`}
                stroke={LINE}
                strokeWidth={2}
                markerEnd="url(#route-arrow)"
              />
            ) : null}
          </g>
        );
      })}
      <defs>
        <marker id="route-arrow" viewBox="0 0 10 10" refX="6" refY="5" markerWidth="5" markerHeight="5" orient="auto">
          <path d="M0,0 L10,5 L0,10 z" fill={LINE} />
        </marker>
      </defs>
    </Figure>
  );
};

const MT_STAGES = [
  { label: "Application", sub: "HTTP API or SMPP" },
  { label: "Access check", sub: "creds, quota, balance" },
  { label: "MT route", sub: "priority + filters" },
  { label: "Connector", sub: "ordered failover list" },
  { label: "SMSC", sub: "carrier network" },
];

/**
 * MTPathDiagram — the five stages every outbound message passes through, in
 * the order the lessons and the console's own screens follow them. Every
 * screen in the console edits or observes exactly one of these boxes.
 */
export const MTPathDiagram = () => {
  const boxWidth = 116;
  const gap = 13;
  const y = 44;
  return (
    <Figure
      viewBox="0 0 640 150"
      title="The end-to-end path of one outbound message"
      description="An application submits a message over the HTTP API or an SMPP bind. Access is checked against credentials, group state, quota and balance. The MT route selects a connector by priority and filters. The connector sends the message upstream in ordered failover order. The SMSC is the carrier or aggregator that accepts it."
      caption={
        <>
          A delivery receipt, if one was requested, does not retrace this path — it comes back
          through a separate correlation step. See the next figure.
        </>
      }
    >
      <defs>
        <marker id="mtpath-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
          <path d="M0,0 L10,5 L0,10 z" fill={OUTBOUND} />
        </marker>
      </defs>
      {MT_STAGES.map((stage, index) => {
        const x = 4 + index * (boxWidth + gap);
        return (
          <g key={stage.label}>
            <Node x={x} y={y} width={boxWidth} label={stage.label} sub={stage.sub} accent={OUTBOUND} />
            {index < MT_STAGES.length - 1 ? (
              <line
                x1={x + boxWidth + 3}
                x2={x + boxWidth + gap - 3}
                y1={y + 30}
                y2={y + 30}
                stroke={OUTBOUND}
                strokeWidth={2}
                markerEnd="url(#mtpath-arrow)"
              />
            ) : null}
          </g>
        );
      })}
    </Figure>
  );
};

/**
 * ReturnPathDiagram — a mobile-originated message and a delivery receipt both
 * arrive as a deliver_sm PDU on the receiver side of the bind and look alike
 * on the wire, but the gateway classifies each one immediately and the two
 * never share a code path afterwards: MO continues into MO-route matching
 * (internal/core/smppc/deliver.go: processDeliver dispatches to
 * processDeliverMO), a receipt is resolved by correlation against the
 * gateway message ID and the DLR URL stored at submit time, and is delivered
 * by a separate worker (processDeliverReceipt; internal/core/dlr,
 * internal/app/dlrthrower) that never consults the MO route table.
 */
const RETURN_ROWS = [
  {
    id: "mo",
    title: "MO",
    note: "mobile-originated",
    y: 74,
    steps: [
      { x: 460, label: "classified as MO" },
      { x: 280, label: "MO route: priority + filters" },
    ],
  },
  {
    id: "dlr",
    title: "DLR",
    note: "delivery receipt",
    y: 154,
    steps: [
      { x: 460, label: "classified as receipt" },
      { x: 280, label: "correlated to gateway ID" },
    ],
  },
];

export const ReturnPathDiagram = () => (
  <Figure
    viewBox="0 0 640 190"
    title="How inbound messages and delivery receipts return to your application"
    description="Both a mobile-originated message and a delivery receipt arrive from the SMSC as a deliver_sm PDU on the receiver side of the bind. The gateway classifies each one immediately on arrival. A mobile-originated message continues to MO route matching and is delivered to an HTTP callback or SMPP client. A delivery receipt is instead correlated to the original gateway message ID and delivered to the callback URL stored at submit time."
    caption={
      <>
        <Legend items={[{ color: INBOUND, label: "Inbound · deliver_sm" }]} />
        Classification happens once, at arrival. A receipt never passes through MO route filters,
        and a mobile-originated message is never resolved by correlation — they only look alike on
        the wire.
      </>
    }
  >
    <defs>
      <marker id="returnpath-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
        <path d="M0,0 L10,5 L0,10 z" fill={INBOUND} />
      </marker>
    </defs>

    <text x={150} y={16} textAnchor="middle" fontSize={11} fill={MUTED}>
      Application
    </text>
    <text x={580} y={16} textAnchor="middle" fontSize={11} fill={MUTED}>
      SMSC
    </text>

    {RETURN_ROWS.map((row) => (
      <g key={row.id}>
        <text x={0} y={row.y + 5} fontSize={13} fontWeight={700} fill={INK}>
          {row.title}
        </text>
        <text x={0} y={row.y + 21} fontSize={10} fill={MUTED}>
          {row.note}
        </text>
        <line
          x1={580}
          x2={150}
          y1={row.y}
          y2={row.y}
          stroke={INBOUND}
          strokeWidth={2}
          markerEnd="url(#returnpath-arrow)"
        />
        {row.steps.map((step) => (
          <g key={step.x}>
            <circle cx={step.x} cy={row.y} r={7} fill={INBOUND} stroke={SURFACE} strokeWidth={2} />
            <text x={step.x} y={row.y - 14} textAnchor="middle" fontSize={10.5} fill={INK}>
              {step.label}
            </text>
          </g>
        ))}
      </g>
    ))}
  </Figure>
);

/**
 * ObjectModelDiagram — the two object graphs an operator builds: account and
 * access (group, user, SMPPs bind) on the left, routing and carriers (route,
 * connector) on the right. Fully neutral — this is structure, not message
 * flow, so it does not borrow the direction hues.
 */
export const ObjectModelDiagram = () => (
  <Figure
    viewBox="0 0 640 290"
    title="The two object graphs behind every message"
    description="Account and access: a group carries a shared balance and quota; every member user draws from it. A user can be mirrored by a separate SMPPs bind account with its own credentials for customers that connect over SMPP. Routing and carriers: a route holds priority and filters and points to an ordered list of connectors; if the first connector is unavailable, the next in the list is tried."
    caption={
      <>
        The left graph decides who may send and how much they may spend. The right graph decides
        where their traffic goes once they are allowed to send it.
      </>
    }
  >
    <line x1={320} y1={8} x2={320} y2={282} stroke={LINE} strokeWidth={1} strokeDasharray="3 5" />

    <text x={150} y={16} textAnchor="middle" fontSize={11} fontWeight={700} fill={MUTED} style={{ textTransform: "uppercase", letterSpacing: "0.06em" }}>
      Account &amp; access
    </text>
    <Node x={70} y={26} width={160} label="Group" sub="shared balance + quota" />
    <line x1={150} y1={86} x2={150} y2={116} stroke={LINE} strokeWidth={2} markerEnd="url(#objectmodel-arrow)" />
    <text x={158} y={104} fontSize={9.5} fill={MUTED}>
      every member draws from it
    </text>
    <Node x={70} y={116} width={160} label="User" sub="HTTP identity + credentials" />
    <line x1={150} y1={176} x2={150} y2={206} stroke={LINE} strokeWidth={2} strokeDasharray="4 4" markerEnd="url(#objectmodel-arrow)" />
    <text x={158} y={194} fontSize={9.5} fill={MUTED}>
      mirrors, on request
    </text>
    <Node x={70} y={206} width={160} label="SMPPs bind account" sub="separate credentials + IP allowlist" />

    <text x={470} y={16} textAnchor="middle" fontSize={11} fontWeight={700} fill={MUTED} style={{ textTransform: "uppercase", letterSpacing: "0.06em" }}>
      Routing &amp; carriers
    </text>
    <Node x={390} y={26} width={160} label="Route" sub="priority + filters" accent={OUTBOUND} />
    <line x1={470} y1={86} x2={470} y2={116} stroke={OUTBOUND} strokeWidth={2} markerEnd="url(#objectmodel-arrow-out)" />
    <text x={478} y={104} fontSize={9.5} fill={MUTED}>
      1st in the list
    </text>
    <Node x={390} y={116} width={160} label="Connector A" sub="tried first" accent={OUTBOUND} />
    <line x1={470} y1={176} x2={470} y2={206} stroke={LINE} strokeWidth={2} strokeDasharray="4 4" markerEnd="url(#objectmodel-arrow)" />
    <text x={478} y={194} fontSize={9.5} fill={MUTED}>
      failover if unavailable
    </text>
    <Node x={390} y={206} width={160} label="Connector B" sub="tried if A cannot take it" />

    <defs>
      <marker id="objectmodel-arrow" viewBox="0 0 10 10" refX="6" refY="5" markerWidth="5" markerHeight="5" orient="auto">
        <path d="M0,0 L10,5 L0,10 z" fill={LINE} />
      </marker>
      <marker id="objectmodel-arrow-out" viewBox="0 0 10 10" refX="6" refY="5" markerWidth="5" markerHeight="5" orient="auto">
        <path d="M0,0 L10,5 L0,10 z" fill={OUTBOUND} />
      </marker>
    </defs>
  </Figure>
);

/**
 * ConnectorStateDiagram — desired state is a stored boolean the admin plane
 * remembers (DesiredStarted); observed state is one of three values reported
 * by the live SMPP session (internal/core/smppc/connector.go: StatusDisconnected
 * / StatusConnecting / StatusBound). The two are read and displayed
 * separately (internal/app/admin/service.go ConnectorView), which is exactly
 * why they can disagree.
 */
export const ConnectorStateDiagram = () => (
  <Figure
    viewBox="0 0 640 250"
    title="Desired state versus observed state on a connector"
    description="Desired state is the boolean the admin plane stores for a connector: started or stopped. Observed state is reported by the live SMPP session and is one of disconnected, connecting or bound. Requesting a start moves the session from disconnected to connecting to bound. Requesting a stop, or losing the connection, moves it back to disconnected. If desired stays started while observed stays disconnected, the cause is one of a fixed set of reasons."
    caption={
      <>
        “Started” describes what you asked for. “Bound” describes what the session is actually
        doing right now. The console shows both, on purpose, so the gap itself is the diagnosis.
      </>
    }
  >
    <defs>
      <marker id="connectorstate-fwd" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto">
        <path d="M0,0 L10,5 L0,10 z" fill={INK} />
      </marker>
      <marker id="connectorstate-back" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto">
        <path d="M0,0 L10,5 L0,10 z" fill={MUTED} />
      </marker>
    </defs>

    <Node x={40} y={30} width={140} label="DISCONNECTED" />
    <Node x={260} y={30} width={140} label="CONNECTING" />
    <Node x={480} y={30} width={140} label="BOUND" />

    <line x1={183} x2={257} y1={60} y2={60} stroke={INK} strokeWidth={2} markerEnd="url(#connectorstate-fwd)" />
    <text x={220} y={52} textAnchor="middle" fontSize={9.5} fill={MUTED}>
      start requested
    </text>
    <line x1={403} x2={477} y1={60} y2={60} stroke={INK} strokeWidth={2} markerEnd="url(#connectorstate-fwd)" />
    <text x={440} y={52} textAnchor="middle" fontSize={9.5} fill={MUTED}>
      bind accepted
    </text>

    <path
      d="M550,90 L550,118 L110,118 L110,90"
      fill="none"
      stroke={MUTED}
      strokeWidth={2}
      strokeDasharray="4 5"
      markerEnd="url(#connectorstate-back)"
    />
    <text x={330} y={132} textAnchor="middle" fontSize={9.5} fill={MUTED}>
      stop requested, or the session drops
    </text>

    <rect x={20} y={155} width={600} height={78} rx={10} fill={SURFACE} stroke={LINE} strokeWidth={2} />
    <text x={40} y={178} fontSize={12.5} fontWeight={700} fill={INK}>
      Started but stuck at DISCONNECTED means one of:
    </text>
    <text x={40} y={200} fontSize={11.5} fill={MUTED}>
      wrong credentials · wrong host or port · a rejected bind
    </text>
    <text x={40} y={218} fontSize={11.5} fill={MUTED}>
      a network block · reconnect backoff
    </text>
  </Figure>
);

const BILLING_BARS: Array<{ label: string; remaining: number; y: number; width: number; height: number; x?: number }> = [
  { label: "Group ceiling", remaining: 0.06, y: 30, width: 560, height: 28 },
  { label: "User A", remaining: 0.82, y: 108, width: 260, height: 24 },
  { label: "User B", remaining: 0.31, y: 108, width: 260, height: 24, x: 340 },
];

/**
 * BillingObjectsDiagram — granted is the outline, remaining is the fill, and
 * the group bar is drawn to the same scale as the two accounts nested under
 * it. Deliberately neutral: this is a balance hierarchy, not a submit/return
 * moment, so it does not borrow ChargingSplitChart's direction hues.
 */
export const BillingObjectsDiagram = () => (
  <Figure
    viewBox="0 0 640 210"
    title="Granted, remaining and the group ceiling"
    description="A group carries its own balance and message quota, shared by every member. A user carries their own balance and quota on top of that. A submit is authorised only when both the user's own remaining balance and the group's remaining balance can cover it, so either one being spent refuses the message."
    caption={
      <>
        User A still has balance of their own, but the group is nearly spent. The next submit from
        either A or B is refused at the group check, not the user's — a healthy personal number does
        not mean the group can still afford it.
      </>
    }
  >
    <text x={40} y={20} fontSize={12} fontWeight={700} fill={INK}>
      {BILLING_BARS[0].label}
    </text>
    <text x={600} y={20} textAnchor="end" fontSize={11} fill={MUTED}>
      {Math.round(BILLING_BARS[0].remaining * 100)}% remaining
    </text>
    <rect x={40} y={BILLING_BARS[0].y} width={BILLING_BARS[0].width} height={BILLING_BARS[0].height} rx={6} fill={SURFACE} stroke={LINE} strokeWidth={2} />
    <rect
      x={40}
      y={BILLING_BARS[0].y}
      width={BILLING_BARS[0].width * BILLING_BARS[0].remaining}
      height={BILLING_BARS[0].height}
      rx={6}
      fill={INBOUND}
    />

    <path d="M60,58 L60,80 L172,80 L172,98" fill="none" stroke={LINE} strokeWidth={2} />
    <path d="M580,58 L580,80 L468,80 L468,98" fill="none" stroke={LINE} strokeWidth={2} />

    {BILLING_BARS.slice(1).map((bar) => {
      const x = bar.x ?? 40;
      return (
        <g key={bar.label}>
          <text x={x} y={92} fontSize={11.5} fontWeight={700} fill={INK}>
            {bar.label}
          </text>
          <text x={x + bar.width} y={92} textAnchor="end" fontSize={10.5} fill={MUTED}>
            {Math.round(bar.remaining * 100)}% remaining
          </text>
          <rect x={x} y={bar.y} width={bar.width} height={bar.height} rx={5} fill={SURFACE} stroke={LINE} strokeWidth={2} />
          <rect x={x} y={bar.y} width={bar.width * bar.remaining} height={bar.height} rx={5} fill={OUTBOUND} />
        </g>
      );
    })}
  </Figure>
);

/**
 * TerminationSinksDiagram — where a terminated message can go, and the one
 * route that was deliberately not built.
 *
 * A terminated message always lands in the spool first; the sink setting only
 * decides who takes it from there. Drawing the declined broker tap as a struck
 * lane is the point of the figure: operators arriving from the old
 * `smsget-jasmin-sms-queues` service look for exactly that path, and its
 * absence is a decision (docs/plans/021), not an oversight.
 */
export const TerminationSinksDiagram = () => (
  <Figure
    viewBox="0 0 640 240"
    title="How a downstream application receives terminated messages"
    description="A partner submits a message. The termination connector decodes it and writes it to the message spool. From the spool there are two supported paths to a downstream application: http-push, where the gateway POSTs each message to your endpoint, and pull, where your application fetches over a cursor API with a scoped read token. A third path, tapping the message broker directly, is deliberately not offered."
    caption={
      <>
        The spool is the single source of truth, so <strong>both</strong> sinks read the same rows —
        which is why a failed push is recoverable by pulling, and why <code>both</code> is a real
        setting rather than a redundancy. Tapping the broker directly is not offered: it recreates
        the coupling the termination connector exists to remove.
      </>
    }
  >
    <defs>
      <marker id="sinks-out" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
        <path d="M0,0 L10,5 L0,10 z" fill={OUTBOUND} />
      </marker>
      <marker id="sinks-in" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
        <path d="M0,0 L10,5 L0,10 z" fill={INBOUND} />
      </marker>
    </defs>

    <Node x={4} y={86} width={124} label="Partner ESME" sub="submit_sm" accent={OUTBOUND} />
    <line x1={132} x2={158} y1={116} y2={116} stroke={OUTBOUND} strokeWidth={2} markerEnd="url(#sinks-out)" />
    <Node x={162} y={86} width={134} label="Termination connector" sub="decode + verdict" accent={OUTBOUND} />
    <line x1={300} x2={326} y1={116} y2={116} stroke={OUTBOUND} strokeWidth={2} markerEnd="url(#sinks-out)" />
    <Node x={330} y={86} width={104} label="Message spool" sub="24h retention" accent={OUTBOUND} />

    {/* http-push: the gateway initiates. */}
    <path d="M434 100 C 470 100, 470 40, 500 40" fill="none" stroke={OUTBOUND} strokeWidth={2} markerEnd="url(#sinks-out)" />
    <Node x={504} y={12} width={132} label="http-push" sub="gateway POSTs to you" accent={OUTBOUND} />

    {/* pull: the application initiates, so the arrow points back at the spool. */}
    <path d="M500 130 C 470 130, 470 118, 438 118" fill="none" stroke={INBOUND} strokeWidth={2} markerEnd="url(#sinks-in)" />
    <Node x={504} y={100} width={132} label="pull" sub="you GET /messages" accent={INBOUND} />

    {/* The path that was declined, drawn struck through rather than omitted. */}
    <path d="M434 132 C 470 132, 470 200, 500 200" fill="none" stroke={LINE} strokeWidth={2} strokeDasharray="5 4" />
    <g opacity={0.55}>
      <Node x={504} y={172} width={132} label="broker tap" sub="not offered" />
      <line x1={508} x2={632} y1={202} y2={202} stroke={MUTED} strokeWidth={2} />
    </g>

    <text x={4} y={224} fontSize={11} fill={MUTED}>
      Sink is a per-connector setting: http-push, pull, both, or none.
    </text>
  </Figure>
);

/**
 * PushDeliveryDiagram — the lifecycle of one http-push attempt.
 *
 * The two facts worth drawing are that the receipt does NOT wait for your
 * endpoint (the partner's DLR is decided by the verdict, not by delivery), and
 * that retries are bounded and end in a dead letter rather than looping.
 */
export const PushDeliveryDiagram = () => (
  <Figure
    viewBox="0 0 640 230"
    title="What happens to one message when the delivery sink is http-push"
    description="The spooled message is picked up by the delivery runner, which POSTs a signed JSON body to your endpoint. A 2xx response marks it delivered. A failure is retried with exponential backoff up to a bounded number of attempts, after which the message is dead-lettered and stays readable in the spool. Separately and in parallel, the receipt runner sends the partner a delivery receipt decided by the verdict, never by whether your endpoint answered."
    caption={
      <>
        The receipt lane is independent on purpose. Your application being down must not turn into
        a <code>REJECTD</code> at the partner — the verdict already decided that, and delivery is a
        durability problem the spool and the DLQ solve separately.
      </>
    }
  >
    <defs>
      <marker id="push-out" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
        <path d="M0,0 L10,5 L0,10 z" fill={OUTBOUND} />
      </marker>
      <marker id="push-in" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
        <path d="M0,0 L10,5 L0,10 z" fill={INBOUND} />
      </marker>
    </defs>

    <Node x={4} y={40} width={116} label="Spool row" sub="pending" accent={OUTBOUND} />
    <line x1={124} x2={150} y1={70} y2={70} stroke={OUTBOUND} strokeWidth={2} markerEnd="url(#push-out)" />
    <Node x={154} y={40} width={124} label="Delivery runner" sub="batched, paced" accent={OUTBOUND} />
    <line x1={282} x2={308} y1={70} y2={70} stroke={OUTBOUND} strokeWidth={2} markerEnd="url(#push-out)" />
    <Node x={312} y={40} width={140} label="POST your endpoint" sub="HMAC-signed JSON" accent={OUTBOUND} />

    <line x1={456} x2={482} y1={70} y2={70} stroke={OUTBOUND} strokeWidth={2} markerEnd="url(#push-out)" />
    <Node x={486} y={40} width={110} label="2xx" sub="delivered" accent={OUTBOUND} />

    {/* Failure lane: bounded retries, then a dead letter. */}
    <path d="M382 102 L 382 132" fill="none" stroke={MUTED} strokeWidth={2} strokeDasharray="5 4" />
    <Node x={312} y={134} width={140} label="retry with backoff" sub="doubling, capped" />
    <line x1={456} x2={482} y1={164} y2={164} stroke={MUTED} strokeWidth={2} />
    <Node x={486} y={134} width={110} label="dead letter" sub="still in spool" />

    {/* The receipt lane, which never touches the push. */}
    <line x1={62} x2={62} y1={104} y2={162} stroke={INBOUND} strokeWidth={2} markerEnd="url(#push-in)" />
    <Node x={4} y={166} width={116} label="Receipt runner" sub="verdict decides" accent={INBOUND} />
    <text x={130} y={198} fontSize={11} fill={MUTED}>
      Independent of the push: the partner&apos;s DLR does not wait for your endpoint.
    </text>
  </Figure>
);

/**
 * PullCursorDiagram — why the pull API pages on an opaque cursor rather than an
 * offset or a timestamp.
 *
 * This is the figure that stops the single most expensive integration mistake:
 * paging by received_at, which silently skips any row whose timestamp the
 * consumer's clock has already passed — a late-reassembled multipart message,
 * or a row re-spooled after a broker redelivery.
 */
export const PullCursorDiagram = () => {
  const rows = [
    { seq: "seq 41", note: "read", cursor: false },
    { seq: "seq 42", note: "read", cursor: false },
    { seq: "seq 43", note: "cursor points here", cursor: true },
    { seq: "seq 44", note: "next page", cursor: false },
  ];
  return (
    <Figure
      viewBox="0 0 640 250"
      title="How the pull API pages, and why it is not an offset"
      description="The spool allocates each row a sequence number in commit order, and re-allocates it whenever the row changes. A pull returns a next_cursor encoding the last sequence seen; passing it back as the after parameter resumes exactly there. Because a mutated row gets a new, higher sequence, a message that was re-spooled or retried after you paged past it is handed back rather than skipped, which paging on a timestamp or an offset would not do."
      caption={
        <>
          Page with <code>?after=&lt;next_cursor&gt;</code> and nothing else. Paging on{" "}
          <code>received_at</code> looks equivalent and is not: a multipart message reassembled late,
          or a row re-spooled after a broker redelivery, carries a timestamp your cursor has already
          passed, and you would never see it.
        </>
      }
    >
      <defs>
        <marker id="pull-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
          <path d="M0,0 L10,5 L0,10 z" fill={INBOUND} />
        </marker>
      </defs>

      <text x={4} y={20} fontSize={12} fontWeight={600} fill={INK}>
        Message spool, ordered by sequence
      </text>
      {rows.map((row, index) => {
        const y = 34 + index * 46;
        const accent = row.cursor ? INBOUND : undefined;
        return (
          <g key={row.seq}>
            <rect x={4} y={y} width={300} height={38} rx={8} fill={SURFACE} stroke={accent ?? LINE} strokeWidth={2} />
            <text x={18} y={y + 24} fontSize={13} fontWeight={600} fill={INK}>
              {row.seq}
            </text>
            <text x={96} y={y + 24} fontSize={11.5} fill={row.cursor ? INBOUND : MUTED}>
              {row.note}
            </text>
          </g>
        );
      })}

      <line x1={308} x2={344} y1={126} y2={126} stroke={INBOUND} strokeWidth={2} markerEnd="url(#pull-arrow)" />
      <Node x={348} y={62} width={140} label="GET /messages" sub="limit, filters" accent={INBOUND} />
      <Node x={348} y={146} width={140} label="next_cursor" sub="opaque, resumable" accent={INBOUND} />
      <path d="M488 176 C 540 176, 540 92, 492 92" fill="none" stroke={INBOUND} strokeWidth={2} strokeDasharray="5 4" markerEnd="url(#pull-arrow)" />
      <text x={500} y={136} fontSize={11} fill={MUTED}>
        pass back
      </text>
      <text x={500} y={152} fontSize={11} fill={MUTED}>
        as ?after=
      </text>

      <text x={4} y={240} fontSize={11} fill={MUTED}>
        A mutated row is re-sequenced, so a retried or re-spooled message is handed back, never skipped.
      </text>
    </Figure>
  );
};
