import { useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Button, Collapse, Empty, Input, Segmented, Tag } from "antd";
import {
  ApiOutlined,
  ArrowRightOutlined,
  BookOutlined,
  BranchesOutlined,
  CheckCircleFilled,
  CloudServerOutlined,
  CodeOutlined,
  CompassOutlined,
  ControlOutlined,
  DeploymentUnitOutlined,
  FieldTimeOutlined,
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
  SwapOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";

import { PageTitle } from "../components/OperatorUI";

type TrackID = "operate" | "network";
type JourneyID = "mt" | "mo";

type Lesson = {
  id: string;
  track: TrackID;
  title: string;
  summary: string;
  outcome: string;
  duration: string;
  icon: ReactNode;
  href?: string;
  action?: string;
};

type FAQItem = {
  key: string;
  category: "Getting started" | "SMPP" | "Routing" | "Reliability";
  question: string;
  answer: ReactNode;
  keywords: string;
};

type GlossaryItem = {
  term: string;
  expansion: string;
  definition: string;
};

const lessons: Lesson[] = [
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
  },
  {
    id: "smpp-roles",
    track: "network",
    title: "SMPP in one picture",
    summary: "SMPP is the session protocol between an application (ESME) and an operator or aggregator (SMSC).",
    outcome: "You can name each side of the connection and its responsibility.",
    duration: "4 min",
    icon: <LinkOutlined />,
  },
  {
    id: "binds",
    track: "network",
    title: "Binds and sessions",
    summary: "TX sends, RX receives, and TRX does both. Enquire-link traffic keeps a session visibly alive.",
    outcome: "You can choose a bind mode and interpret a disconnected session.",
    duration: "5 min",
    icon: <DeploymentUnitOutlined />,
  },
  {
    id: "directions",
    track: "network",
    title: "MT, MO and delivery receipts",
    summary: "MT travels toward a handset, MO starts at a handset, and a DLR reports a later delivery state.",
    outcome: "You no longer confuse message direction with the connection direction.",
    duration: "5 min",
    icon: <SwapOutlined />,
  },
  {
    id: "addressing",
    track: "network",
    title: "Addresses, TON and NPI",
    summary: "Source and destination values are interpreted with type-of-number and numbering-plan metadata.",
    outcome: "You know why the same digits may route differently when metadata changes.",
    duration: "4 min",
    icon: <CodeOutlined />,
  },
  {
    id: "encoding",
    track: "network",
    title: "Encoding and segmentation",
    summary: "GSM 7-bit fits more characters than UCS-2; long messages become linked segments over the network.",
    outcome: "You can explain why one user message may be billed as several SMS parts.",
    duration: "5 min",
    icon: <MessageOutlined />,
  },
  {
    id: "throughput",
    track: "network",
    title: "Throughput, throttling and retries",
    summary: "TPS limits protect network capacity; throttled traffic must slow down and retry without duplication.",
    outcome: "You can distinguish temporary back-pressure from a permanent rejection.",
    duration: "5 min",
    icon: <FieldTimeOutlined />,
  },
];

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
};

const searchableText = (...values: Array<string | undefined>) => values.join(" ").toLowerCase();

export const EducationPage = () => {
  const [track, setTrack] = useState<TrackID>("operate");
  const [journey, setJourney] = useState<JourneyID>("mt");
  const [query, setQuery] = useState("");
  const normalizedQuery = query.trim().toLowerCase();

  const searchResults = useMemo(() => {
    if (!normalizedQuery) return null;
    return {
      lessons: lessons.filter((lesson) =>
        searchableText(lesson.title, lesson.summary, lesson.outcome).includes(normalizedQuery),
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
  }, [normalizedQuery]);

  const visibleLessons = lessons.filter((lesson) => lesson.track === track);
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
            <strong>From first bind to first message</strong>
            <p>Six practical lessons · about 32 minutes</p>
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
                  {lesson.href ? (
                    <Link to={lesson.href} className="education-text-link">
                      {lesson.action} <ArrowRightOutlined />
                    </Link>
                  ) : null}
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
                <article className="lesson-card" key={lesson.id}>
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
                    {lesson.href ? (
                      <Link to={lesson.href} className="education-text-link">
                        {lesson.action} <ArrowRightOutlined />
                      </Link>
                    ) : null}
                  </div>
                </article>
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
    </div>
  );
};
