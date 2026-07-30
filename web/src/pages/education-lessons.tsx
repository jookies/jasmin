import type { ReactNode } from "react";
import {
  AccountBookOutlined,
  ApiOutlined,
  AuditOutlined,
  BranchesOutlined,
  CodeOutlined,
  CompassOutlined,
  DeploymentUnitOutlined,
  DollarOutlined,
  ExclamationCircleOutlined,
  FieldTimeOutlined,
  FilterOutlined,
  GlobalOutlined,
  LinkOutlined,
  MessageOutlined,
  SafetyCertificateOutlined,
  SwapOutlined,
  TeamOutlined,
  WalletOutlined,
} from "@ant-design/icons";

import {
  BillingObjectsDiagram,
  ChargingSplitChart,
  ConnectorStateDiagram,
  DLRLevelsDiagram,
  MTPathDiagram,
  ObjectModelDiagram,
  ReturnPathDiagram,
  RoutePriorityDiagram,
  SMPPSessionDiagram,
  SegmentationChart,
} from "../components/EducationDiagrams";

export type TrackID = "operate" | "network";

export type LessonStep = {
  title: string;
  detail: string;
};

export type Lesson = {
  id: string;
  track: TrackID;
  title: string;
  summary: string;
  outcome: string;
  duration: string;
  icon: ReactNode;
  href?: string;
  action?: string;
  /** Why this matters, in the operator's terms. */
  why: string;
  steps: LessonStep[];
  /** The thing that catches people out. Every lesson has one; say it. */
  watchOut: string;
  requiresInterceptors?: boolean;
  /** One or more inline figures that belong to this lesson, rendered in the detail view. */
  figure?: ReactNode;
  /** IDs of other lessons worth reading next, shown as a sidebar cross-link. */
  related?: string[];
};

/**
 * Lesson content lives here rather than in the page so the page stays about
 * layout. Every claim describes THIS gateway's behaviour — including the
 * inherited quirks — because a lesson that teaches the general SMPP case and
 * leaves the local surprise out is how an operator gets hurt.
 */
export const lessons: Lesson[] = [
  {
    id: "control-room",
    track: "operate",
    title: "Read the control room",
    summary: "Start with gateway health, connector state and recent traffic before changing anything.",
    outcome: "You can separate a platform issue from a connector or routing issue.",
    duration: "3 min",
    icon: <CompassOutlined />,
    href: "/",
    action: "Open control room",
    why: "Most 'the gateway is broken' reports are one connector, one route or one customer's quota. The dashboard tells you which layer to look at before you change anything.",
    steps: [
      {
        title: "Check readiness first",
        detail: "The health panel reports the gateway's dependencies — PostgreSQL, the broker, and each required connector's bind state. If a dependency is down, nothing below it is worth debugging.",
      },
      {
        title: "Then connector state",
        detail: "A connector has a desired state (what you asked for) and an observed state (what the SMPP session is actually doing). They differ whenever credentials, the network or reconnect backoff get in the way.",
      },
      {
        title: "Then traffic counters",
        detail: "Per-connector counters move with bind, submit, throttle and inbound events. A connector that is bound but shows no submits is a routing question, not a connection one.",
      },
      {
        title: "Only then change something",
        detail: "Make one change, watch the same three panels, and keep the ability to explain what moved.",
      },
    ],
    watchOut:
      "Some panels report values the gateway does not yet measure. Per-connector clocks render 'ND' and several modern metrics have no recorder, so a zero there means 'not measured', not 'nothing happened'.",
    related: ["connectors", "operate-safely"],
  },
  {
    id: "connectors",
    track: "operate",
    title: "Connect an upstream SMSC",
    summary: "Create an SMPP connector, understand desired versus observed state, then confirm the bind.",
    outcome: "You know why “Started” does not always mean “Bound”.",
    duration: "6 min",
    icon: <ApiOutlined />,
    href: "/connectors",
    action: "Open connectors",
    why: "The connector is your side of the carrier relationship. Everything outbound depends on it reaching BOUND and staying there.",
    steps: [
      {
        title: "Create it with the credentials the carrier issued",
        detail: "Host, port, system ID, password and bind mode come from the carrier. The password is write-only here: it is never sent back to the browser, and leaving it blank on an edit keeps the stored one.",
      },
      {
        title: "Start it, then watch the observed state",
        detail: "Started is your intent, stored by the admin plane. Bound is what the live session reports. The gap between them is where the diagnosis lives.",
      },
      {
        title: "Read the failure honestly",
        detail: "Wrong credentials, wrong host or port, a rejected bind, a network block or reconnect backoff each keep a connector Started and not Bound. The connector log names which.",
      },
      {
        title: "Confirm with real traffic",
        detail: "A bind proves the session. Only a submit that comes back ESME_ROK proves the account is allowed to send.",
      },
    ],
    watchOut:
      "A connector must be stopped before it can be deleted, and a connector declared in the gateway's config file cannot be edited or started from here at all — it is owned by the file.",
    figure: <ConnectorStateDiagram />,
    related: ["binds", "smpp-roles", "operate-safely"],
  },
  {
    id: "access",
    track: "operate",
    title: "Model customer access",
    summary: "Use groups for shared policy, gateway users for HTTP traffic and SMPPs users for ESME binds.",
    outcome: "Credentials, quotas and permissions sit in the right layer.",
    duration: "5 min",
    icon: <TeamOutlined />,
    href: "/users",
    action: "Open users",
    why: "Getting this layering right once means later changes — a price change, a suspension, a new brand under the same contract — are one edit instead of many.",
    steps: [
      {
        title: "Group first, if the customer is more than one account",
        detail: "A group carries a shared balance and message quota. Every member draws from it, so a group is how you cap a whole contract rather than one login.",
      },
      {
        title: "Gateway user for HTTP and the identity of the account",
        detail: "This holds the password, the balance, the quota, the permissions and the value filters. It is the account the billing engine knows about.",
      },
      {
        title: "SMPPs bind account for customers that connect over SMPP",
        detail: "Separate object, separate credentials, plus the IP allowlist and maximum concurrent binds. Creating a gateway user can mirror one for you.",
      },
      {
        title: "Set permissions and filters deliberately",
        detail: "Authorizations decide what a customer may do; value filters constrain what they may send — destination, source, priority, content.",
      },
    ],
    watchOut:
      "A customer is refused when either their own ceiling or their group's is spent, so a healthy personal balance under an exhausted group still cannot send.",
    figure: <ObjectModelDiagram />,
    related: ["billing-accounts", "smpps-binds"],
  },
  {
    id: "mt-routing",
    track: "operate",
    title: "Build the outbound path",
    summary: "Order MT routes by priority, attach filters and send matching traffic to a connector.",
    outcome: "You can predict which SMSC will receive a submitted message.",
    duration: "7 min",
    icon: <BranchesOutlined />,
    href: "/routes",
    action: "Open MT routes",
    why: "The route decides both the carrier and the price. A wrong route is a wrong invoice as well as a wrong delivery path.",
    steps: [
      {
        title: "Start with a default route at order 0",
        detail: "The default carries no filters and catches everything that matched nothing else. Without one, any submit that matches no route is refused at the front door.",
      },
      {
        title: "Add specific routes above it",
        detail: "Higher order is evaluated first. Put the narrow rules — a destination prefix, a customer group — above the broad fallback.",
      },
      {
        title: "Set the rate on each route",
        detail: "The rate is per message part, and it is what the customer is charged when their traffic takes that route.",
      },
      {
        title: "Verify by quoting a destination",
        detail: "The rate tool prices the route a destination would actually take, so it is a routing check as much as a pricing one.",
      },
    ],
    watchOut:
      "Filter patterns are anchored: `555` means 'starts with 555', not 'contains'. Use `.*555` for a substring. And a route listing several connectors is ordered failover — it does not share traffic between them.",
    figure: (
      <>
        <MTPathDiagram />
        <RoutePriorityDiagram />
      </>
    ),
    related: ["mo-routing", "libraries", "smpp-status", "charging-models"],
  },
  {
    id: "mo-routing",
    track: "operate",
    title: "Deliver inbound traffic",
    summary: "Use MO routes to forward mobile-originated messages and receipts to HTTP or SMPP destinations.",
    outcome: "Replies and delivery receipts reach the application that needs them.",
    duration: "6 min",
    icon: <GlobalOutlined />,
    href: "/mo-routes",
    action: "Open MO routes",
    why: "Inbound failure is quieter than outbound failure: an unroutable MO is logged and dropped, and nobody gets an error.",
    steps: [
      {
        title: "Have a default MO route",
        detail: "Same rule as MT, higher stakes. Inbound traffic that matches nothing disappears silently rather than failing visibly.",
      },
      {
        title: "Choose the destination kind",
        detail: "Either an HTTP callback on the customer's side, or a bound SMPP client of yours. Saved HTTP destinations let you reuse one URL across routes.",
      },
      {
        title: "Filter by connector and content when you need to",
        detail: "A route can match on which carrier the message arrived through as well as on the message itself.",
      },
      {
        title: "Test the whole path",
        detail: "Inject an inbound message and confirm the customer's endpoint received it and acknowledged it.",
      },
    ],
    watchOut:
      "A callback only counts as delivered if it answers HTTP 2xx *and* a body that trims to exactly `ACK/Jasmin`. A 200 with an empty body is a failure and will be retried.",
    figure: <ReturnPathDiagram />,
    related: ["mt-routing", "libraries", "receipts-in-practice"],
  },
  {
    id: "libraries",
    track: "operate",
    title: "Reuse filters and destinations",
    summary: "Save a filter or an HTTP destination once, then attach it to as many routes as you need.",
    outcome: "Route changes stop being copy-paste, and one edit reaches every route that uses it.",
    duration: "4 min",
    icon: <FilterOutlined />,
    href: "/filters",
    action: "Open saved filters",
    why: "The same 'is this customer' or 'is this destination range' test tends to appear in several routes. Naming it once makes the routes readable and the change safe.",
    steps: [
      {
        title: "Create the named filter",
        detail: "Pick the type — destination, source, content, user, group, date or time window — and give it a name that says what it means, not what it matches.",
      },
      {
        title: "Insert it into a route",
        detail: "The route form can pull from the library instead of you retyping the pattern.",
      },
      {
        title: "Save HTTP destinations the same way",
        detail: "A named GET or POST URL template can be copied into any MO route that should deliver there.",
      },
    ],
    watchOut:
      "The library can create more filter types than the route editor can insert. If a saved filter will not attach to a route, that is the reason — not a broken filter.",
    related: ["mt-routing", "mo-routing"],
  },
  {
    id: "smpps-binds",
    track: "operate",
    title: "Host a customer's SMPP bind",
    summary: "Issue bind credentials, restrict them by IP and bind count, then unbind or ban a live session.",
    outcome: "You can onboard an ESME customer and cut one off without touching the others.",
    duration: "6 min",
    icon: <LinkOutlined />,
    href: "/smpps-users",
    action: "Open SMPPs binds",
    why: "When the customer connects to you rather than the other way round, you are the SMSC. That means you own authentication, session limits and disconnection.",
    steps: [
      {
        title: "Create the bind account",
        detail: "A system ID and password, separate from the customer's HTTP credentials even when they belong to the same account.",
      },
      {
        title: "Constrain it",
        detail: "Restrict the source IP and cap the number of concurrent binds so one customer cannot exhaust your session capacity.",
      },
      {
        title: "Set the submit and receipt policy",
        detail: "What they may set on a submit — source address, priority, DLR level — is policy on this account, not something to negotiate per message.",
      },
      {
        title: "Know your two eject buttons",
        detail: "Unbind drops the live sessions; the customer can reconnect immediately. Ban disables the account first, so the reconnect fails too.",
      },
    ],
    watchOut:
      "An SMPP 3.4 bind password is capped at eight characters by the protocol. A longer generated secret cannot bind at all, which looks like a credential problem and is really a length problem.",
    figure: <SMPPSessionDiagram />,
    related: ["binds", "smpp-roles", "access"],
  },
  {
    id: "billing-accounts",
    track: "operate",
    title: "Fund and watch an account",
    summary: "Grant a balance and a message quota, then read what the customer has left right now.",
    outcome: "You can tell a spent account from a small one, and a customer's ceiling from their group's.",
    duration: "6 min",
    icon: <WalletOutlined />,
    href: "/billing/accounts",
    action: "Open billing accounts",
    why: "Two numbers look alike and mean opposite things: what you granted, and what is left. Reading the wrong one is how a customer gets suspended for having spent their money exactly as intended.",
    steps: [
      {
        title: "Grant the ceiling on the user or the group",
        detail: "A balance caps money; a submit_sm_count caps messages. Either can stop traffic, and a null value means unlimited — which is not the same as zero.",
      },
      {
        title: "Read granted against remaining",
        detail: "Granted comes from the account's provisioned spec. Remaining comes from the live billing engine and moves with every charge.",
      },
      {
        title: "Check the group ceiling too",
        detail: "The group's shared balance is displayed beside the user's, because an exhausted group stops a member with money left.",
      },
      {
        title: "Top up by raising the grant",
        detail: "Editing the account's balance in the admin plane changes the live value directly. It does not reset what has been spent.",
      },
    ],
    watchOut:
      "A customer declared in the gateway's config file cannot be topped up from here at all — the admin plane reserves that identity. Their balance can only fall until you edit the file and restart, and re-granting the same number is a no-op because nothing in the spec changed.",
    figure: (
      <>
        <BillingObjectsDiagram />
        <ChargingSplitChart />
      </>
    ),
    related: ["billing-usage", "charging-models", "access"],
  },
  {
    id: "billing-usage",
    track: "operate",
    title: "Answer “what happened to my message?”",
    summary: "Look a gateway message ID up against the commercial record and read its event timeline.",
    outcome: "You can separate an SMSC rejection from a delivery failure, and say what it cost.",
    duration: "7 min",
    icon: <AuditOutlined />,
    href: "/billing/usage",
    action: "Open usage records",
    why: "This is the complaint workflow. The customer has one ID and a question, and the answer has to distinguish 'we never sent it', 'the carrier refused it' and 'the carrier took it and never delivered'.",
    steps: [
      {
        title: "Search by the message ID you returned",
        detail: "One ID returns every part of that message. A long message is several parts, each with its own outcome and its own charge.",
      },
      {
        title: "Read the two states separately",
        detail: "Submission state says what the SMSC did with it. Delivery state says what the handset did, and stays empty until a receipt arrives.",
      },
      {
        title: "Open the event timeline",
        detail: "Admission, each attempt, the SMSC answer, the final receipt and the late-billing settlement, in order, with timestamps.",
      },
      {
        title: "Export the evidence",
        detail: "The same window can be downloaded as CSV or JSONL, byte-identical to what any other caller would get.",
      },
    ],
    watchOut:
      "These records are content-free by design: no destination number, sender or message text is stored. You cannot search by recipient — only by customer, message ID, time window or SMSC ID.",
    related: ["billing-statements", "receipts-in-practice"],
  },
  {
    id: "billing-statements",
    track: "operate",
    title: "Produce a usage statement",
    summary: "Aggregate a customer's rated traffic over a window and export the records behind the total.",
    outcome: "You can hand accounting a defensible number and the evidence for it.",
    duration: "5 min",
    icon: <AccountBookOutlined />,
    href: "/billing/statements",
    action: "Open statements",
    why: "An invoice line is only worth as much as your ability to defend it. The statement is the number; the export is the evidence behind the number.",
    steps: [
      {
        title: "Choose the customer and the window",
        detail: "The window is required — an unbounded aggregate is a full-table scan pretending to be a report. Leave the customer blank for every account at once.",
      },
      {
        title: "Read charged apart from quoted",
        detail: "Charged is money actually taken: the submit-time decrement plus what the late-billing ledger applied. Quoted-but-unapplied late money is listed separately because an intent is not revenue.",
      },
      {
        title: "Reconcile against the records",
        detail: "Export the same window and sum the charged column. It must equal the statement total; if it does not, one of the two is wrong and worth investigating before invoicing.",
      },
      {
        title: "Note the currency",
        detail: "Totals are grouped per currency, so a currency changed mid-window produces two rows rather than one wrong number.",
      },
    ],
    watchOut:
      "Money taken at submit is never refunded when the SMSC later rejects the part, so a charged total can legitimately exceed the delivered count. That is the billing model, not a discrepancy.",
    related: ["billing-usage", "charging-models"],
  },
  {
    id: "interceptors",
    track: "operate",
    title: "Rewrite traffic with an interceptor",
    summary: "Run a script before a message is encoded, to mutate it or reject it outright.",
    outcome: "You can normalise or block traffic without changing routes — and you know the risk.",
    duration: "6 min",
    icon: <CodeOutlined />,
    href: "/interceptors",
    action: "Open interceptors",
    why: "Some rules do not fit a filter: rewriting a sender, stripping a prefix, refusing a keyword. An interceptor is the escape hatch, and it runs as code on the gateway host.",
    steps: [
      {
        title: "Pick the direction",
        detail: "An MT interceptor runs on the outbound path before the message is encoded. An MO interceptor runs on inbound traffic, including reassembled multipart messages.",
      },
      {
        title: "Attach filters so it runs on the right traffic",
        detail: "An interceptor matches the same way a route does — put it only on the traffic that needs it.",
      },
      {
        title: "Decide between mutate and reject",
        detail: "A script can change the message and let it continue, or refuse it with a status the front door reports back.",
      },
    ],
    watchOut:
      "Interceptor editing is disabled by default and for good reason: with a console session it is arbitrary code execution on the gateway host. Treat the script list as production code, not configuration.",
    related: ["mt-routing", "mo-routing"],
  },
  {
    id: "operate-safely",
    track: "operate",
    title: "Operate and recover safely",
    summary: "Inspect live counters, test a message and save a known-good configuration profile.",
    outcome: "You can diagnose changes and return to a stable state.",
    duration: "5 min",
    icon: <SafetyCertificateOutlined />,
    href: "/operations",
    action: "Open live operations",
    why: "Confidence to change things comes from being able to undo them and from being able to see whether the change did what you expected.",
    steps: [
      {
        title: "Save a profile before a risky change",
        detail: "A named profile snapshots the admin-managed tables. It is your rollback point, and it is worth taking before rather than during an incident.",
      },
      {
        title: "Make one change, then look",
        detail: "Live counters and connector state tell you whether the change had the effect you expected, or a different one.",
      },
      {
        title: "Send a controlled test",
        detail: "The test tool goes through the real pipeline — real routing, real charging, real carrier. It is a true end-to-end check precisely because it is not a simulation.",
      },
    ],
    watchOut:
      "The test tool consumes the customer's quota and real carrier credit, and requesting a receipt without a callback URL is refused rather than silently accepted.",
    related: ["control-room"],
  },
  {
    id: "smpp-roles",
    track: "network",
    title: "SMPP in one picture",
    summary: "SMPP is the session protocol between an application (ESME) and an operator or aggregator (SMSC).",
    outcome: "You can name each side of the connection and its responsibility.",
    duration: "4 min",
    icon: <LinkOutlined />,
    why: "Almost every SMPP conversation goes wrong because the two sides mean different things by 'client'. The roles are fixed and worth naming precisely.",
    steps: [
      {
        title: "The ESME is the application side",
        detail: "External Short Messaging Entity — anything that connects in to send or receive messages. When you connect to a carrier, you are the ESME.",
      },
      {
        title: "The SMSC is the network side",
        detail: "It accepts responsibility for messages and hands them toward the mobile network. When your customer connects to you, you are the SMSC.",
      },
      {
        title: "This gateway is both, at once",
        detail: "It binds outward to carriers as an ESME and accepts inward binds from customers as an SMSC. That is why there are two separate account types.",
      },
    ],
    watchOut:
      "The ESME always opens the TCP connection, in every bind mode — including a receiver bind, where all the messages travel the other way.",
    figure: <SMPPSessionDiagram />,
    related: ["binds", "connectors"],
  },
  {
    id: "binds",
    track: "network",
    title: "Binds and sessions",
    summary: "TX sends, RX receives, and TRX does both. Enquire-link traffic keeps a session visibly alive.",
    outcome: "You can choose a bind mode and interpret a disconnected session.",
    duration: "5 min",
    icon: <DeploymentUnitOutlined />,
    why: "The bind mode is a contract about which side may send which PDUs. Choosing it wrongly produces a session that connects and then cannot carry the traffic you need.",
    steps: [
      {
        title: "Bind is a login, not a connection",
        detail: "The TCP connection comes first; the bind authenticates it and declares the mode. A connected-but-unbound socket carries nothing.",
      },
      {
        title: "Choose the mode from the carrier's contract",
        detail: "Transceiver when one session may do both. Transmitter plus receiver when the provider separates outbound submits from inbound traffic.",
      },
      {
        title: "Enquire-link keeps it honest",
        detail: "Periodic keepalives prove the session is alive rather than merely open. A silent TCP connection can be dead for minutes before anyone notices.",
      },
    ],
    watchOut:
      "A receiver bind cannot carry submits, so it is excluded from outbound routing entirely — but it can still be chosen as a destination for inbound traffic and receipts.",
    figure: <SMPPSessionDiagram />,
    related: ["smpp-roles", "connectors", "smpps-binds"],
  },
  {
    id: "directions",
    track: "network",
    title: "MT, MO and delivery receipts",
    summary: "MT travels toward a handset, MO starts at a handset, and a DLR reports a later delivery state.",
    outcome: "You no longer confuse message direction with the connection direction.",
    duration: "5 min",
    icon: <SwapOutlined />,
    why: "Direction words in SMS describe the handset, not your network. That single fact removes most of the confusion.",
    steps: [
      {
        title: "MT is mobile-terminated",
        detail: "It ends at a handset. Everything your customers submit is MT, regardless of which side opened the connection.",
      },
      {
        title: "MO is mobile-originated",
        detail: "It starts at a handset — a reply, a keyword, an opt-out. It arrives on the receiving side of a bind.",
      },
      {
        title: "A DLR is neither, exactly",
        detail: "A delivery receipt travels the same inbound path as MO traffic but reports on an earlier MT message, which is why it needs correlation to be useful.",
      },
    ],
    watchOut:
      "A successful submit means the SMSC accepted responsibility, nothing more. Delivery is a separate, later, independent fact — and one that may never arrive.",
    figure: <DLRLevelsDiagram />,
    related: ["receipts-in-practice", "mo-routing"],
  },
  {
    id: "addressing",
    track: "network",
    title: "Addresses, TON and NPI",
    summary: "Source and destination values are interpreted with type-of-number and numbering-plan metadata.",
    outcome: "You know why the same digits may route differently when metadata changes.",
    duration: "4 min",
    icon: <MessageOutlined />,
    why: "The same digits with different metadata are different addresses to a carrier. This is a common cause of 'the number is right but it will not deliver'.",
    steps: [
      {
        title: "TON says what kind of number it is",
        detail: "International, national, alphanumeric and others. An alphanumeric sender — a brand name — is a TON value, not a special field.",
      },
      {
        title: "NPI says which numbering plan reads it",
        detail: "ISDN/E.164 is the usual answer for real phone numbers.",
      },
      {
        title: "Carriers are strict about the pairing",
        detail: "A carrier may reject or silently re-write a submit whose TON/NPI does not match what it expects for that address format.",
      },
    ],
    watchOut:
      "Defaults matter as much as explicit values. A connector provisioned without addressing settings still emits a specific TON/NPI pair on the wire, and it may not be the one your carrier assumes.",
    related: ["mt-routing"],
  },
  {
    id: "encoding",
    track: "network",
    title: "Encoding and segmentation",
    summary: "GSM 7-bit fits more characters than UCS-2; long messages become linked segments over the network.",
    outcome: "You can explain why one user message may be billed as several SMS parts.",
    duration: "5 min",
    icon: <MessageOutlined />,
    why: "Segmentation is where a customer's idea of 'one message' and your invoice stop agreeing. Being able to explain it turns a dispute into an explanation.",
    steps: [
      {
        title: "The character set sets the capacity",
        detail: "GSM 7-bit fits 160 characters in one part; UCS-2, needed for most non-Latin text and many emoji, fits 70.",
      },
      {
        title: "Concatenation costs capacity",
        detail: "Linking parts together requires a header inside the message body, dropping the per-part capacity to 153 and 67 respectively.",
      },
      {
        title: "Each part is a message to the network",
        detail: "Parts are transmitted, charged and receipted individually, then reassembled by the handset.",
      },
    ],
    watchOut:
      "A single non-GSM character — a curly quote pasted from a word processor — switches the whole message to UCS-2 and can turn one part into three.",
    figure: <SegmentationChart />,
    related: ["charging-models"],
  },
  {
    id: "throughput",
    track: "network",
    title: "Throughput, throttling and retries",
    summary: "TPS limits protect network capacity; throttled traffic must slow down and retry without duplication.",
    outcome: "You can distinguish temporary back-pressure from a permanent rejection.",
    duration: "5 min",
    icon: <FieldTimeOutlined />,
    why: "Throttling is the network asking you to slow down. Treating it as a failure — or retrying it too eagerly — turns a manageable delay into duplicate messages or a blocked account.",
    steps: [
      {
        title: "Rate limits exist on both sides",
        detail: "The carrier enforces what your contract allows; you enforce what each customer is allowed to send you.",
      },
      {
        title: "Throttled is retryable, rejected is not",
        detail: "A throttle status means try again shortly. A validation or account rejection will fail identically forever.",
      },
      {
        title: "Retry without duplicating",
        detail: "Any retry must be safe to repeat, or a slow carrier becomes a customer receiving the same message twice.",
      },
    ],
    watchOut:
      "The per-user ceiling here rejects an over-rate submit rather than queueing it, has no burst allowance, and a quota of 0 means unlimited rather than blocked.",
    related: ["smpp-status"],
  },
  {
    id: "receipts-in-practice",
    track: "network",
    title: "Delivery receipts in practice",
    summary: "Levels 1, 2 and 3 request different events, and a callback only counts as delivered when the receiver says so.",
    outcome: "You can choose a receipt level and explain why a callback was retried.",
    duration: "6 min",
    icon: <SwapOutlined />,
    why: "Receipts are the most common integration failure, and almost always for one of two reasons: the wrong level was requested, or the receiving endpoint does not acknowledge correctly.",
    steps: [
      {
        title: "Level 1 acknowledges the submit",
        detail: "One callback when the SMSC answers. It proves acceptance, not delivery.",
      },
      {
        title: "Level 2 reports the final state",
        detail: "One callback later, carrying DELIVRD, EXPIRED, UNDELIV or similar — and none at all if the submit itself failed.",
      },
      {
        title: "Level 3 is both",
        detail: "The acknowledgement immediately, then the final state when the carrier reports it. Choose this when you need to tell 'rejected' from 'still waiting'.",
      },
      {
        title: "Acknowledge every callback properly",
        detail: "The receiving endpoint must answer HTTP 2xx and a body that trims to exactly `ACK/Jasmin`, or the delivery is treated as failed and retried.",
      },
    ],
    watchOut:
      "Requesting receipts without supplying a callback URL is refused here rather than accepted, because the alternative is a success response followed by a receipt that can never arrive.",
    figure: <DLRLevelsDiagram />,
    related: ["directions", "billing-usage", "mo-routing"],
  },
  {
    id: "smpp-status",
    track: "network",
    title: "Read an SMPP status code",
    summary: "ESME_ROK is acceptance; the rest divide into slow down, fix the message, and fix the account.",
    outcome: "You can tell a retryable rejection from one that will fail forever.",
    duration: "5 min",
    icon: <ExclamationCircleOutlined />,
    why: "The status code is the carrier telling you exactly what is wrong. Reading it saves the hours usually spent guessing.",
    steps: [
      {
        title: "ESME_ROK means accepted",
        detail: "Responsibility has passed to the SMSC. It says nothing about delivery.",
      },
      {
        title: "Throttling means retry later",
        detail: "You are over your agreed rate. Back off; the message is fine.",
      },
      {
        title: "Message faults mean fix the content",
        detail: "Invalid destination, invalid source, bad length or bad encoding will fail identically on every retry.",
      },
      {
        title: "Account faults mean fix the relationship",
        detail: "Binding refused, not authorised for that destination, or credit exhausted — these are commercial problems wearing a protocol error.",
      },
    ],
    watchOut:
      "Carriers vary in which code they choose for the same condition, and some return a generic system error for anything they do not want to explain. Treat the code as the strongest hint, not a contract.",
    related: ["throughput"],
  },
  {
    id: "charging-models",
    track: "network",
    title: "How SMS traffic is charged",
    summary: "Prepaid takes the money at submit, postpaid on acceptance, and every segment is priced on its own.",
    outcome: "You can explain a customer's invoice line before they dispute it.",
    duration: "6 min",
    icon: <DollarOutlined />,
    why: "Charging happens at two moments, and which moment matters decides what a rejected or undelivered message costs.",
    steps: [
      {
        title: "The rate comes from the route",
        detail: "Not from the customer and not from the destination directly — from whichever route the message matched.",
      },
      {
        title: "Every part is priced separately",
        detail: "A three-part message costs three times the rate. This is where segmentation becomes a billing question.",
      },
      {
        title: "Prepaid takes it all at submit",
        detail: "The balance falls when the message is admitted, before the carrier has answered.",
      },
      {
        title: "A split defers part of it",
        detail: "A configured percentage is taken at submit and the rest only after the SMSC accepts, applied once by an idempotent ledger.",
      },
    ],
    watchOut:
      "The early share is not returned when the SMSC rejects the message. A rejected part keeps its submit-time charge and simply never incurs the later one.",
    figure: (
      <>
        <SegmentationChart />
        <ChargingSplitChart />
      </>
    ),
    related: ["billing-accounts", "billing-statements"],
  },
];
