import { useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Alert, Button, Collapse, Drawer, Empty, Input, Segmented, Tag } from "antd";
import {
  ApiOutlined,
  ArrowRightOutlined,
  AuditOutlined,
  BookOutlined,
  BranchesOutlined,
  CheckCircleFilled,
  CloudServerOutlined,
  ControlOutlined,
  FileSearchOutlined,
  GlobalOutlined,
  InfoCircleOutlined,
  LinkOutlined,
  MessageOutlined,
  QuestionCircleOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  SendOutlined,
  SettingOutlined,
  ThunderboltOutlined,
  WalletOutlined,
} from "@ant-design/icons";

import { PageTitle } from "../components/OperatorUI";
import { useFeatures } from "../useFeatures";
import { lessons, type Lesson, type TrackID } from "./education-lessons";
import {
  ChargingSplitChart,
  DLRLevelsDiagram,
  RoutePriorityDiagram,
  SMPPSessionDiagram,
  SegmentationChart,
} from "../components/EducationDiagrams";
import "./education.css";

type JourneyID = "mt" | "mo" | "dlr";

type FAQItem = {
  key: string;
  category: "Getting started" | "SMPP" | "Routing" | "Billing" | "Reliability";
  question: string;
  answer: ReactNode;
  keywords: string;
};

type GlossaryItem = {
  term: string;
  expansion: string;
  definition: string;
};

const faqItems: FAQItem[] = [
  {
    key: "start",
    category: "Getting started",
    question: "What should I configure first?",
    answer: (
      <>
        Start with one connector and confirm that it reaches <strong>Bound</strong>. Then create a
        group and user, add one broad MT route, and send a controlled test message. Add filters and
        more routes only after the simple path works.
      </>
    ),
    keywords: "first setup connector user group test",
  },
  {
    key: "started-bound",
    category: "Getting started",
    question: "Why is a connector Started but not Bound?",
    answer:
      "Started is the desired state stored by the admin plane. Bound is the state observed from the live SMPP session. Wrong credentials, host or port, a rejected bind, a network problem, or reconnect backoff can keep the two states different.",
    keywords: "connector desired observed started bound disconnected credentials",
  },
  {
    key: "bind-mode",
    category: "SMPP",
    question: "Which bind mode should I use?",
    answer:
      "Use transceiver (TRX) when the provider supports one bidirectional session. Use transmitter (TX) plus receiver (RX) when the SMSC contract separates outbound submits from inbound messages and delivery receipts.",
    keywords: "tx rx trx transmitter receiver transceiver",
  },
  {
    key: "dlr",
    category: "SMPP",
    question: "Does submit success mean the handset received the SMS?",
    answer:
      "No. A successful submit means the SMSC accepted responsibility for the message. A delivery receipt can later report delivered, expired, rejected or another final state—if receipts were requested and the upstream network provides them.",
    keywords: "submit success delivery handset dlr receipt accepted",
  },
  {
    key: "priority",
    category: "Routing",
    question: "How does route priority work?",
    answer:
      "Routes are evaluated in priority order. The first route whose filters match wins, so specific routes should usually sit above a broad fallback route. Overlapping filters are a common reason traffic uses the wrong connector.",
    keywords: "route priority order filters match fallback",
  },
  {
    key: "mt-mo",
    category: "Routing",
    question: "What is the practical difference between MT and MO routes?",
    answer:
      "MT routes choose an upstream connector for messages submitted by your customers or applications. MO routes choose where inbound deliver_sm traffic goes—typically an HTTP callback or a bound SMPP client.",
    keywords: "mt mo outbound inbound deliver sm route",
  },
  {
    key: "segments",
    category: "Reliability",
    question: "Why did one text create multiple SMS parts?",
    answer:
      "Character set and length determine segmentation. Unicode commonly uses UCS-2 and has a smaller per-part capacity. Concatenation headers also consume space, so a long message can become two or more separately transmitted and billed parts.",
    keywords: "unicode ucs2 gsm7 long message segment billing",
  },
  {
    key: "filter-regex",
    category: "Routing",
    question: "My filter pattern matches nothing. Why?",
    answer: (
      <>
        Patterns are anchored at position zero even without <code>^</code>, because the inherited
        engine uses <code>re.match</code> semantics. So <code>555</code> means “starts with 555”, not
        “contains 555”. For a substring match write <code>.*555</code>.
      </>
    ),
    keywords: "filter regex pattern match anchor substring re.match destination",
  },
  {
    key: "pool-order",
    category: "Routing",
    question: "Does a multi-connector route load-balance?",
    answer:
      "No. The runtime picks the first available connector in list order, so the second one is a failover target rather than a share of the traffic. Order the list by preference, and do not expect an even split.",
    keywords: "pool failover random round robin load balance connector order",
  },
  {
    key: "throughput-zero",
    category: "Reliability",
    question: "I set a throughput of 0 to block a customer. What happens?",
    answer: (
      <>
        They send at full speed. A quota of <strong>0 means unlimited</strong>, not blocked — an
        inherited behaviour kept on purpose because live integrations depend on it. To stop a
        customer, disable the account instead. Note also that the ceiling rejects an over-rate
        submit rather than queueing it, and there is no burst allowance.
      </>
    ),
    keywords: "throughput quota zero unlimited block throttle tps qos suspend",
  },
  {
    key: "callback-ack",
    category: "Reliability",
    question: "Why does the gateway keep retrying my callback?",
    answer: (
      <>
        A callback counts as successful only when it returns an HTTP 2xx <em>and</em> a body that,
        after trimming whitespace, is exactly <code>ACK/Jasmin</code>. A 200 with an empty body, or
        the right body with a 500, are both failures and are retried.
      </>
    ),
    keywords: "callback dlr mo retry ack jasmin 200 body http failure",
  },
  {
    key: "charged-rejected",
    category: "Billing",
    question: "The SMSC rejected a message. Was the customer still charged?",
    answer: (
      <>
        Partly, and by design. The early portion is taken when the message is admitted, before the
        SMSC answers, and it is not refunded on rejection. The late portion is only applied after an
        acceptance, so a rejected message keeps the early amount and never incurs the late one. On a
        fully prepaid account the whole rate is early.
      </>
    ),
    keywords: "charge rejected refund early late decrement billing prepaid split money",
  },
  {
    key: "granted-remaining",
    category: "Billing",
    question: "Why do the balance columns show two different numbers?",
    answer:
      "Granted is what the account was provisioned with; remaining is what the live billing engine says is left. They diverge as soon as the customer sends anything. Editing the grant does not reset the remaining value, and topping an account up means raising the grant.",
    keywords: "granted remaining balance provisioned live quota difference top up",
  },
  {
    key: "currency-xxx",
    category: "Billing",
    question: "Why is the currency shown as XXX?",
    answer: (
      <>
        <code>XXX</code> is the ISO 4217 code for “no currency”, and it is the deliberate default:
        inherited route rates are unitless numbers. Until an operator sets a settlement currency in
        the gateway configuration, treat the amounts as rate units rather than money.
      </>
    ),
    keywords: "currency xxx iso 4217 money rate unit settlement cdr",
  },
  {
    key: "cdr-destination",
    category: "Billing",
    question: "Can I search usage records by destination number?",
    answer:
      "No, and that is a design decision rather than a gap. Commercial records deliberately store no destination, sender or message content — only routing and billing metadata. Investigate by customer, gateway message ID, time window or SMSC message ID instead.",
    keywords: "cdr search destination number privacy content msisdn record",
  },
  {
    key: "changes",
    category: "Reliability",
    question: "How should I protect a working configuration?",
    answer:
      "Make one change at a time, verify it with live state and a controlled test, then save a named profile. A profile gives operators a deliberate recovery point; live counters help confirm whether the change had the expected effect.",
    keywords: "profile backup save recover config operations",
  },
];

const glossary: GlossaryItem[] = [
  { term: "SMSC", expansion: "Short Message Service Center", definition: "The upstream network system that stores, forwards and reports on SMS traffic." },
  { term: "ESME", expansion: "External Short Messaging Entity", definition: "An application or gateway that connects to an SMSC over SMPP." },
  { term: "PDU", expansion: "Protocol Data Unit", definition: "A structured SMPP command or response exchanged over a session." },
  { term: "MT", expansion: "Mobile Terminated", definition: "A message traveling toward a mobile subscriber." },
  { term: "MO", expansion: "Mobile Originated", definition: "A message that originated from a mobile subscriber." },
  { term: "DLR", expansion: "Delivery Receipt", definition: "A later status update about a submitted message or segment." },
  { term: "TON / NPI", expansion: "Type of Number / Numbering Plan Indicator", definition: "Metadata that tells the network how to interpret an address." },
  { term: "TPS", expansion: "Transactions per second", definition: "The agreed or enforced throughput rate for SMPP operations." },
  { term: "Bind", expansion: "Authenticated SMPP session", definition: "The login handshake that establishes TX, RX or TRX capabilities." },
  { term: "CDR", expansion: "Call Detail Record", definition: "The durable commercial record of one charged message part: route, connector, rate, amounts and final state." },
  { term: "UDH", expansion: "User Data Header", definition: "A header inside the message body that links the segments of a long SMS together." },
  { term: "SAR", expansion: "Segmentation and Reassembly", definition: "The alternative to UDH: dedicated SMPP fields carrying the same segment numbering." },
  { term: "TLV", expansion: "Tag-Length-Value", definition: "An optional SMPP parameter appended to a PDU, used for vendor and extended features." },
  { term: "ACK/Jasmin", expansion: "Callback acknowledgement", definition: "The exact body your endpoint must return, with an HTTP 2xx, for a receipt or inbound message to count as delivered." },
  { term: "Early / late decrement", expansion: "Split charging", definition: "The share of the rate taken at submit versus after the SMSC accepts the message." },
];

const journeys: Record<JourneyID, Array<{ title: string; detail: string; icon: ReactNode }>> = {
  mt: [
    { title: "Application submits", detail: "HTTP API or a bound SMPP user sends the destination and content.", icon: <SendOutlined /> },
    { title: "Access is checked", detail: "Credentials, group state, permissions, quota and balance are evaluated.", icon: <SafetyCertificateOutlined /> },
    { title: "MT route matches", detail: "Priority and filters select the connector and effective rate.", icon: <BranchesOutlined /> },
    { title: "SMPP submit", detail: "Jasmin encodes one or more submit_sm PDUs and sends them upstream.", icon: <ApiOutlined /> },
    { title: "Network delivers", detail: "The SMSC hands traffic into the mobile network toward the handset.", icon: <CloudServerOutlined /> },
    { title: "Receipt returns", detail: "If requested, a DLR follows the inbound path back to the application.", icon: <CheckCircleFilled /> },
  ],
  mo: [
    { title: "Handset sends", detail: "The subscriber sends a reply or the network creates a delivery receipt.", icon: <MessageOutlined /> },
    { title: "SMSC forwards", detail: "The upstream SMSC sends a deliver_sm PDU over the receiver side of the bind.", icon: <CloudServerOutlined /> },
    { title: "Jasmin classifies", detail: "Message type, connector, address and tags become routing inputs.", icon: <SettingOutlined /> },
    { title: "MO route matches", detail: "Priority and filters choose the first valid downstream destination.", icon: <BranchesOutlined /> },
    { title: "Application receives", detail: "Traffic is delivered to an HTTP callback or a bound SMPP client.", icon: <GlobalOutlined /> },
  ],
  dlr: [
    { title: "Submit is answered", detail: "submit_sm_resp carries the SMSC message ID. At level 1 or 3 this alone produces a callback.", icon: <ApiOutlined /> },
    { title: "Correlation is stored", detail: "The SMSC ID is tied to your gateway message ID so a later receipt can find it.", icon: <LinkOutlined /> },
    { title: "Carrier reports", detail: "Minutes or hours later the network sends a receipt with a final state: DELIVRD, EXPIRED, UNDELIV and so on.", icon: <CloudServerOutlined /> },
    { title: "Receipt is matched", detail: "The receipt is resolved back to the original message, and the commercial record gains its delivery outcome.", icon: <AuditOutlined /> },
    { title: "Late money settles", detail: "On a split account the remaining share of the rate is applied once, idempotently.", icon: <WalletOutlined /> },
    { title: "Application is told", detail: "Your callback receives level=2 and must answer ACK/Jasmin, or the delivery is retried.", icon: <CheckCircleFilled /> },
  ],
};

const searchableText = (...values: Array<string | undefined>) => values.join(" ").toLowerCase();

/**
 * A figure in the standalone gallery below still belongs to the lesson(s)
 * that explain it — this renders the "used in" chips that jump straight to
 * them, so the gallery never reads as disconnected from the lessons again.
 */
const FigureLessonLinks = ({ ids, onOpen }: { ids: string[]; onOpen: (lesson: Lesson) => void }) => {
  const matches = ids.map((id) => lessons.find((lesson) => lesson.id === id)).filter((lesson): lesson is Lesson => Boolean(lesson));
  if (matches.length === 0) return null;
  return (
    <div className="education-figure-links">
      <span>Used in</span>
      {matches.map((lesson) => (
        <button
          key={lesson.id}
          type="button"
          className="education-figure-link"
          onClick={() => onOpen(lesson)}
        >
          {lesson.title}
        </button>
      ))}
    </div>
  );
};

/** Related-lesson entries in the drawer sidebar, resolved and filtered live. */
const RelatedLessons = ({
  ids,
  available,
  onOpen,
}: {
  ids: string[] | undefined;
  available: Lesson[];
  onOpen: (lesson: Lesson) => void;
}) => {
  if (!ids || ids.length === 0) return null;
  const matches = ids
    .map((id) => available.find((lesson) => lesson.id === id))
    .filter((lesson): lesson is Lesson => Boolean(lesson));
  if (matches.length === 0) return null;
  return (
    <div className="lesson-detail-related">
      <h4>Related lessons</h4>
      <div className="lesson-related-list">
        {matches.map((lesson) => (
          <button
            key={lesson.id}
            type="button"
            className="lesson-related-link"
            onClick={() => onOpen(lesson)}
            aria-label={`Open the lesson: ${lesson.title}`}
          >
            <span>{lesson.title}</span>
            <ArrowRightOutlined aria-hidden="true" />
          </button>
        ))}
      </div>
    </div>
  );
};

export const EducationPage = () => {
  const features = useFeatures();
  const [track, setTrack] = useState<TrackID>("operate");
  const [journey, setJourney] = useState<JourneyID>("mt");
  const [openLesson, setOpenLesson] = useState<Lesson | null>(null);
  const [query, setQuery] = useState("");
  const normalizedQuery = query.trim().toLowerCase();

  // A lesson about a screen this deployment hides would send the reader to a
  // 404. Interceptor management is opt-in because the scripts run as code on
  // the gateway host.
  const availableLessons = useMemo(
    () => lessons.filter((lesson) => !lesson.requiresInterceptors || features.interceptor_editing),
    [features.interceptor_editing],
  );

  const searchResults = useMemo(() => {
    if (!normalizedQuery) return null;
    return {
      lessons: availableLessons.filter((lesson) =>
        searchableText(
          lesson.title,
          lesson.summary,
          lesson.outcome,
          lesson.why,
          lesson.watchOut,
          ...lesson.steps.flatMap((step) => [step.title, step.detail]),
        ).includes(normalizedQuery),
      ),
      faq: faqItems.filter((item) =>
        searchableText(item.category, item.question, item.keywords, String(item.answer)).includes(
          normalizedQuery,
        ),
      ),
      glossary: glossary.filter((item) =>
        searchableText(item.term, item.expansion, item.definition).includes(normalizedQuery),
      ),
    };
  }, [normalizedQuery, availableLessons]);

  const visibleLessons = availableLessons.filter((lesson) => lesson.track === track);
  // The recommended-start card counts the operator track rather than hard-coding
  // a number, so adding a lesson cannot leave the hero quietly lying.
  const operatorLessons = availableLessons.filter((lesson) => lesson.track === "operate");
  const operatorMinutes = operatorLessons.reduce(
    (total, lesson) => total + (Number.parseInt(lesson.duration, 10) || 0),
    0,
  );
  const resultCount = searchResults
    ? searchResults.lessons.length + searchResults.faq.length + searchResults.glossary.length
    : 0;

  return (
    <div className="page-container education-page">
      <section className="education-hero">
        <div className="education-hero-copy">
          <PageTitle
            eyebrow="Education center"
            title="Understand the system, not just the screens"
            description="Learn the operator workflow and the telecom ideas behind it. Short, practical explanations connect every Jasmin object to the journey of a real SMS."
          />
          <label className="education-search">
            <span className="sr-only">Search guides, FAQ and glossary</span>
            <Input
              size="large"
              allowClear
              prefix={<SearchOutlined aria-hidden="true" />}
              placeholder="Search “DLR”, “route priority”, “bind”…"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </label>
        </div>
        <aside className="education-start-card">
          <span className="education-start-icon" aria-hidden="true">
            <BookOutlined />
          </span>
          <div>
            <span>Recommended start</span>
            <strong>From first bind to first invoice</strong>
            <p>
              {operatorLessons.length} practical lessons · about {operatorMinutes} minutes
            </p>
          </div>
          <Button
            type="primary"
            onClick={() => {
              setQuery("");
              setTrack("operate");
              document.getElementById("learning-path")?.scrollIntoView({ behavior: "smooth" });
            }}
          >
            Start learning
          </Button>
        </aside>
      </section>

      {searchResults ? (
        <section className="education-search-results" aria-live="polite">
          <div className="section-heading">
            <div>
              <span className="section-kicker">Search</span>
              <h2>{resultCount} result{resultCount === 1 ? "" : "s"} for “{query.trim()}”</h2>
            </div>
            <Button onClick={() => setQuery("")}>Clear search</Button>
          </div>
          {resultCount === 0 ? (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description="Try a broader term such as connector, route, DLR or message."
            />
          ) : (
            <div className="education-result-list">
              {searchResults.lessons.map((lesson) => (
                <article className="education-result-card" key={lesson.id}>
                  <Tag color={lesson.track === "operate" ? "cyan" : "blue"}>
                    {lesson.track === "operate" ? "System guide" : "Telecom concept"}
                  </Tag>
                  <div>
                    <h3>{lesson.title}</h3>
                    <p>{lesson.summary}</p>
                  </div>
                  <button
                    type="button"
                    className="education-text-link is-button"
                    onClick={() => setOpenLesson(lesson)}
                  >
                    Read the lesson <ArrowRightOutlined />
                  </button>
                </article>
              ))}
              {searchResults.faq.map((item) => (
                <article className="education-result-card" key={item.key}>
                  <Tag>FAQ · {item.category}</Tag>
                  <div>
                    <h3>{item.question}</h3>
                    <p>{item.answer}</p>
                  </div>
                </article>
              ))}
              {searchResults.glossary.map((item) => (
                <article className="education-result-card" key={item.term}>
                  <Tag>Glossary</Tag>
                  <div>
                    <h3>{item.term} · {item.expansion}</h3>
                    <p>{item.definition}</p>
                  </div>
                </article>
              ))}
            </div>
          )}
        </section>
      ) : (
        <>
          <section className="education-track-panel" id="learning-path">
            <div className="section-heading education-track-heading">
              <div>
                <span className="section-kicker">Guided learning</span>
                <h2>Choose your path</h2>
                <p>Follow the system path for hands-on setup, or the network path for the ideas underneath it.</p>
              </div>
              <Segmented
                size="large"
                value={track}
                onChange={(value) => setTrack(value as TrackID)}
                options={[
                  { value: "operate", label: "Operate Jasmin", icon: <ControlOutlined /> },
                  { value: "network", label: "Understand the network", icon: <ThunderboltOutlined /> },
                ]}
              />
            </div>

            <div className="learning-path">
              {visibleLessons.map((lesson, index) => (
                <button
                  type="button"
                  className="lesson-card"
                  key={lesson.id}
                  onClick={() => setOpenLesson(lesson)}
                  aria-label={`Open the lesson: ${lesson.title}`}
                >
                  <div className="lesson-step" aria-label={`Step ${index + 1}`}>
                    {String(index + 1).padStart(2, "0")}
                  </div>
                  <div className="lesson-icon" aria-hidden="true">{lesson.icon}</div>
                  <div className="lesson-copy">
                    <div className="lesson-meta">
                      <span>{lesson.duration}</span>
                      <span>{track === "operate" ? "Hands-on" : "Concept"}</span>
                    </div>
                    <h3>{lesson.title}</h3>
                    <p>{lesson.summary}</p>
                    <div className="lesson-outcome">
                      <CheckCircleFilled aria-hidden="true" />
                      <span>{lesson.outcome}</span>
                    </div>
                    <span className="education-text-link">
                      Read the lesson <ArrowRightOutlined />
                    </span>
                  </div>
                </button>
              ))}
            </div>
          </section>

          <section className="education-journey-panel">
            <div className="section-heading journey-heading">
              <div>
                <span className="section-kicker">Architecture without the fog</span>
                <h2>Follow one message end to end</h2>
                <p>Every screen in the console controls or observes one step in this path.</p>
              </div>
              <Segmented
                value={journey}
                onChange={(value) => setJourney(value as JourneyID)}
                options={[
                  { value: "mt", label: "MT · outbound" },
                  { value: "mo", label: "MO · inbound" },
                  { value: "dlr", label: "DLR · receipt" },
                ]}
              />
            </div>

            <ol className="message-journey">
              {journeys[journey].map((step, index) => (
                <li key={step.title}>
                  <div className="journey-icon" aria-hidden="true">{step.icon}</div>
                  <span className="journey-order">{index + 1}</span>
                  <h3>{step.title}</h3>
                  <p>{step.detail}</p>
                  {index < journeys[journey].length - 1 ? (
                    <ArrowRightOutlined className="journey-arrow" aria-hidden="true" />
                  ) : null}
                </li>
              ))}
            </ol>

            <div className="plane-explainer">
              <article>
                <ControlOutlined aria-hidden="true" />
                <div>
                  <strong>Control plane</strong>
                  <p>Users, routes, filters, connectors and profiles describe what the gateway should do.</p>
                </div>
              </article>
              <article>
                <ThunderboltOutlined aria-hidden="true" />
                <div>
                  <strong>Data plane</strong>
                  <p>Live HTTP and SMPP sessions authenticate, route, charge and move each message.</p>
                </div>
              </article>
              <div className="plane-note">
                <InfoCircleOutlined aria-hidden="true" />
                <span>Desired configuration and observed runtime state can differ. The console shows both so operators can diagnose the gap.</span>
              </div>
            </div>
          </section>

          <section className="education-figure-panel">
            <div className="section-heading compact" style={{ padding: 0, border: "none" }}>
              <div>
                <span className="section-kicker">Pictures over paragraphs</span>
                <h2>The five things that are easier to see</h2>
                <p>
                  Each figure states one behaviour that regularly surprises people — including a
                  couple this gateway inherited on purpose.
                </p>
              </div>
            </div>

            <div className="education-figure-grid">
              <article className="education-figure-card is-wide">
                <h3>What a bind actually is</h3>
                <p>Which side may send PDUs — not who dialled the connection.</p>
                <SMPPSessionDiagram />
                <FigureLessonLinks ids={["smpp-roles", "binds", "smpps-binds"]} onOpen={setOpenLesson} />
              </article>

              <article className="education-figure-card is-wide">
                <h3>Why a route you added never fires</h3>
                <p>Evaluation stops at the first match, from the highest order down.</p>
                <RoutePriorityDiagram />
                <FigureLessonLinks ids={["mt-routing"]} onOpen={setOpenLesson} />
              </article>

              <article className="education-figure-card">
                <h3>Why one text became three messages</h3>
                <p>Capacity per part, by encoding.</p>
                <SegmentationChart />
                <FigureLessonLinks ids={["encoding", "charging-models"]} onOpen={setOpenLesson} />
              </article>

              <article className="education-figure-card">
                <h3>When the money is taken</h3>
                <p>One rate, split between submit time and acceptance.</p>
                <ChargingSplitChart />
                <FigureLessonLinks ids={["billing-accounts", "charging-models"]} onOpen={setOpenLesson} />
              </article>

              <article className="education-figure-card is-wide">
                <h3>What each receipt level actually delivers</h3>
                <p>And why level 2 can leave you with silence.</p>
                <DLRLevelsDiagram />
                <FigureLessonLinks ids={["receipts-in-practice", "directions"]} onOpen={setOpenLesson} />
              </article>
            </div>
          </section>

          <section className="education-reference-grid">
            <div className="education-faq-panel">
              <div className="section-heading compact">
                <div>
                  <span className="section-kicker">Common questions</span>
                  <h2>FAQ for real operations</h2>
                </div>
                <QuestionCircleOutlined className="section-heading-icon" aria-hidden="true" />
              </div>
              <Collapse
                ghost
                expandIconPosition="end"
                items={faqItems.map((item) => ({
                  key: item.key,
                  label: (
                    <span className="faq-label">
                      <small>{item.category}</small>
                      <strong>{item.question}</strong>
                    </span>
                  ),
                  children: <p className="faq-answer">{item.answer}</p>,
                }))}
              />
            </div>

            <aside className="education-glossary-panel">
              <div className="section-heading compact">
                <div>
                  <span className="section-kicker">Keep nearby</span>
                  <h2>Essential glossary</h2>
                </div>
                <FileSearchOutlined className="section-heading-icon" aria-hidden="true" />
              </div>
              <div className="glossary-list">
                {glossary.map((item) => (
                  <article key={item.term}>
                    <span>{item.term}</span>
                    <div>
                      <strong>{item.expansion}</strong>
                      <p>{item.definition}</p>
                    </div>
                  </article>
                ))}
              </div>
            </aside>
          </section>
        </>
      )}

      <Drawer
        open={openLesson !== null}
        onClose={() => setOpenLesson(null)}
        width="70%"
        rootClassName="lesson-drawer"
        title={openLesson?.title}
        destroyOnHidden
      >
        {openLesson ? (
          <div className="lesson-detail">
            <div className="lesson-detail-meta">
              <Tag color={openLesson.track === "operate" ? "cyan" : "blue"}>
                {openLesson.track === "operate" ? "System guide" : "Telecom concept"}
              </Tag>
              <span>{openLesson.duration}</span>
            </div>

            <div className="lesson-detail-layout">
              <div className="lesson-detail-columns">
                <div className="lesson-detail-main">
                  <p className="lesson-detail-lead">{openLesson.why}</p>

                  {openLesson.figure ? (
                    <div className="lesson-detail-figures">{openLesson.figure}</div>
                  ) : null}

                  <span className="lesson-detail-subhead">How it works</span>
                  <ol className="lesson-detail-steps">
                    {openLesson.steps.map((step) => (
                      <li key={step.title}>
                        <strong>{step.title}</strong>
                        <p>{step.detail}</p>
                      </li>
                    ))}
                  </ol>
                </div>

                <aside className="lesson-detail-sidebar">
                  <div className="lesson-detail-outcome">
                    <CheckCircleFilled aria-hidden="true" />
                    <span>{openLesson.outcome}</span>
                  </div>

                  <Alert type="warning" showIcon message="Watch out" description={openLesson.watchOut} />

                  {openLesson.href ? (
                    <Link to={openLesson.href} onClick={() => setOpenLesson(null)}>
                      <Button type="primary" icon={<ArrowRightOutlined />}>
                        {openLesson.action}
                      </Button>
                    </Link>
                  ) : null}

                  <RelatedLessons ids={openLesson.related} available={availableLessons} onOpen={setOpenLesson} />
                </aside>
              </div>
            </div>
          </div>
        ) : null}
      </Drawer>
    </div>
  );
};
