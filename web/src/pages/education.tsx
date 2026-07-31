import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import {
  Alert,
  Button,
  Collapse,
  Drawer,
  Empty,
  Input,
  Popconfirm,
  Segmented,
  Tag,
} from "antd";
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

type CurriculumSectionDefinition = {
  id: string;
  title: string;
  description: string;
  lessonIds: readonly string[];
};

type CurriculumSectionView = CurriculumSectionDefinition & {
  lessons: Lesson[];
  minutes: number;
};

type LessonPosition = {
  index: number;
  total: number;
  sectionTitle: string;
  previous?: Lesson;
  next?: Lesson;
};

const EDUCATION_PROGRESS_KEY = "synevyr.education.completed-lessons.v1";

const trackNames: Record<TrackID, string> = {
  operate: "Operate Jasmin",
  network: "Understand the network",
};

/**
 * Curriculum structure belongs to the learning-center presentation rather
 * than lesson prose. Explicit ID lists keep the intended path stable if the
 * data file is reordered, while the fallback in buildTrackSections prevents
 * a newly-added lesson from disappearing if it has not yet been classified.
 */
const curriculum: Record<TrackID, readonly CurriculumSectionDefinition[]> = {
  operate: [
    {
      id: "orient",
      title: "Orient and connect",
      description:
        "Read system state, establish the upstream session and model customer access.",
      lessonIds: ["control-room", "connectors", "access"],
    },
    {
      id: "route",
      title: "Route live traffic",
      description:
        "Build outbound and inbound paths, then reuse their shared objects safely.",
      lessonIds: ["mt-routing", "mo-routing", "libraries", "smpps-binds"],
    },
    {
      id: "prove",
      title: "Prove and bill",
      description:
        "Follow a message into balances, usage records and customer statements.",
      lessonIds: ["billing-accounts", "billing-usage", "billing-statements"],
    },
    {
      id: "extend",
      title: "Extend and recover",
      description:
        "Add advanced delivery behavior and finish with a repeatable recovery practice.",
      lessonIds: [
        "interceptors",
        "termination-sinks",
        "termination-push",
        "termination-pull",
        "operate-safely",
      ],
    },
  ],
  network: [
    {
      id: "smpp",
      title: "SMPP foundations",
      description:
        "Learn the parties, sessions and message directions before reading packet-level behavior.",
      lessonIds: ["smpp-roles", "binds", "directions"],
    },
    {
      id: "payload",
      title: "Payload and capacity",
      description:
        "Understand addresses, encoding, segmentation and throughput limits.",
      lessonIds: ["addressing", "encoding", "throughput"],
    },
    {
      id: "settlement",
      title: "Delivery and settlement",
      description:
        "Interpret receipts and status codes, then connect delivery behavior to charging.",
      lessonIds: ["receipts-in-practice", "smpp-status", "charging-models"],
    },
  ],
};

const parseMinutes = (duration: string) =>
  Number.parseInt(duration, 10) || 0;

const readCompletedLessonIds = (): string[] => {
  if (typeof window === "undefined") return [];

  try {
    const stored = window.localStorage.getItem(EDUCATION_PROGRESS_KEY);
    if (!stored) return [];

    const parsed: unknown = JSON.parse(stored);
    if (!Array.isArray(parsed)) return [];

    return parsed.filter(
      (value): value is string => typeof value === "string",
    );
  } catch {
    // Storage is a convenience. Private mode or a damaged value must not make
    // the operator console unusable.
    return [];
  }
};

const buildTrackSections = (
  track: TrackID,
  available: Lesson[],
): CurriculumSectionView[] => {
  const trackLessons = available.filter((lesson) => lesson.track === track);
  const byId = new Map(trackLessons.map((lesson) => [lesson.id, lesson]));
  const classified = new Set<string>();

  const sections = curriculum[track]
    .map((definition) => {
      const sectionLessons = definition.lessonIds.flatMap((id) => {
        const lesson = byId.get(id);
        if (!lesson) return [];

        classified.add(id);
        return [lesson];
      });

      return {
        ...definition,
        lessons: sectionLessons,
        minutes: sectionLessons.reduce(
          (total, lesson) => total + parseMinutes(lesson.duration),
          0,
        ),
      };
    })
    .filter((section) => section.lessons.length > 0);

  const additionalLessons = trackLessons.filter(
    (lesson) => !classified.has(lesson.id),
  );

  if (additionalLessons.length === 0) return sections;

  return [
    ...sections,
    {
      id: "additional",
      title: "Additional lessons",
      description:
        "New material that has not yet been assigned to a curriculum section.",
      lessonIds: additionalLessons.map((lesson) => lesson.id),
      lessons: additionalLessons,
      minutes: additionalLessons.reduce(
        (total, lesson) => total + parseMinutes(lesson.duration),
        0,
      ),
    },
  ];
};

const faqItems: FAQItem[] = [
  {
    key: "start",
    category: "Getting started",
    question: "What should I configure first?",
    answer: (
      <>
        Start with one connector and confirm that it reaches{" "}
        <strong>Bound</strong>. Then create a group and user, add one broad MT
        route, and send a controlled test message. Add filters and more routes
        only after the simple path works.
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
    keywords:
      "connector desired observed started bound disconnected credentials",
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
        Patterns are anchored at position zero even without <code>^</code>,
        because the inherited engine uses <code>re.match</code> semantics. So{" "}
        <code>555</code> means “starts with 555”, not “contains 555”. For a
        substring match write <code>.*555</code>.
      </>
    ),
    keywords:
      "filter regex pattern match anchor substring re.match destination",
  },
  {
    key: "pool-order",
    category: "Routing",
    question: "Does a multi-connector route load-balance?",
    answer:
      "No. The runtime picks the first available connector in list order, so the second one is a failover target rather than a share of the traffic. Order the list by preference, and do not expect an even split.",
    keywords:
      "pool failover random round robin load balance connector order",
  },
  {
    key: "throughput-zero",
    category: "Reliability",
    question: "I set a throughput of 0 to block a customer. What happens?",
    answer: (
      <>
        They send at full speed. A quota of{" "}
        <strong>0 means unlimited</strong>, not blocked — an inherited
        behaviour kept on purpose because live integrations depend on it. To
        stop a customer, disable the account instead. Note also that the ceiling
        rejects an over-rate submit rather than queueing it, and there is no
        burst allowance.
      </>
    ),
    keywords:
      "throughput quota zero unlimited block throttle tps qos suspend",
  },
  {
    key: "callback-ack",
    category: "Reliability",
    question: "Why does the gateway keep retrying my callback?",
    answer: (
      <>
        A callback counts as successful only when it returns an HTTP 2xx{" "}
        <em>and</em> a body that, after trimming whitespace, is exactly{" "}
        <code>ACK/Jasmin</code>. A 200 with an empty body, or the right body with
        a 500, are both failures and are retried.
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
        Partly, and by design. The early portion is taken when the message is
        admitted, before the SMSC answers, and it is not refunded on rejection.
        The late portion is only applied after an acceptance, so a rejected
        message keeps the early amount and never incurs the late one. On a fully
        prepaid account the whole rate is early.
      </>
    ),
    keywords:
      "charge rejected refund early late decrement billing prepaid split money",
  },
  {
    key: "granted-remaining",
    category: "Billing",
    question: "Why do the balance columns show two different numbers?",
    answer:
      "Granted is what the account was provisioned with; remaining is what the live billing engine says is left. They diverge as soon as the customer sends anything. Editing the grant does not reset the remaining value, and topping an account up means raising the grant.",
    keywords:
      "granted remaining balance provisioned live quota difference top up",
  },
  {
    key: "currency-xxx",
    category: "Billing",
    question: "Why is the currency shown as XXX?",
    answer: (
      <>
        <code>XXX</code> is the ISO 4217 code for “no currency”, and it is the
        deliberate default: inherited route rates are unitless numbers. Until
        an operator sets a settlement currency in the gateway configuration,
        treat the amounts as rate units rather than money.
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
    keywords:
      "cdr search destination number privacy content msisdn record",
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
  {
    term: "SMSC",
    expansion: "Short Message Service Center",
    definition:
      "The upstream network system that stores, forwards and reports on SMS traffic.",
  },
  {
    term: "ESME",
    expansion: "External Short Messaging Entity",
    definition:
      "An application or gateway that connects to an SMSC over SMPP.",
  },
  {
    term: "PDU",
    expansion: "Protocol Data Unit",
    definition:
      "A structured SMPP command or response exchanged over a session.",
  },
  {
    term: "MT",
    expansion: "Mobile Terminated",
    definition: "A message traveling toward a mobile subscriber.",
  },
  {
    term: "MO",
    expansion: "Mobile Originated",
    definition: "A message that originated from a mobile subscriber.",
  },
  {
    term: "DLR",
    expansion: "Delivery Receipt",
    definition:
      "A later status update about a submitted message or segment.",
  },
  {
    term: "TON / NPI",
    expansion: "Type of Number / Numbering Plan Indicator",
    definition:
      "Metadata that tells the network how to interpret an address.",
  },
  {
    term: "TPS",
    expansion: "Transactions per second",
    definition:
      "The agreed or enforced throughput rate for SMPP operations.",
  },
  {
    term: "Bind",
    expansion: "Authenticated SMPP session",
    definition:
      "The login handshake that establishes TX, RX or TRX capabilities.",
  },
  {
    term: "CDR",
    expansion: "Call Detail Record",
    definition:
      "The durable commercial record of one charged message part: route, connector, rate, amounts and final state.",
  },
  {
    term: "UDH",
    expansion: "User Data Header",
    definition:
      "A header inside the message body that links the segments of a long SMS together.",
  },
  {
    term: "SAR",
    expansion: "Segmentation and Reassembly",
    definition:
      "The alternative to UDH: dedicated SMPP fields carrying the same segment numbering.",
  },
  {
    term: "TLV",
    expansion: "Tag-Length-Value",
    definition:
      "An optional SMPP parameter appended to a PDU, used for vendor and extended features.",
  },
  {
    term: "ACK/Jasmin",
    expansion: "Callback acknowledgement",
    definition:
      "The exact body your endpoint must return, with an HTTP 2xx, for a receipt or inbound message to count as delivered.",
  },
  {
    term: "Early / late decrement",
    expansion: "Split charging",
    definition:
      "The share of the rate taken at submit versus after the SMSC accepts the message.",
  },
];

const journeys: Record<
  JourneyID,
  Array<{ title: string; detail: string; icon: ReactNode }>
> = {
  mt: [
    {
      title: "Application submits",
      detail:
        "HTTP API or a bound SMPP user sends the destination and content.",
      icon: <SendOutlined />,
    },
    {
      title: "Access is checked",
      detail:
        "Credentials, group state, permissions, quota and balance are evaluated.",
      icon: <SafetyCertificateOutlined />,
    },
    {
      title: "MT route matches",
      detail:
        "Priority and filters select the connector and effective rate.",
      icon: <BranchesOutlined />,
    },
    {
      title: "SMPP submit",
      detail:
        "Jasmin encodes one or more submit_sm PDUs and sends them upstream.",
      icon: <ApiOutlined />,
    },
    {
      title: "Network delivers",
      detail:
        "The SMSC hands traffic into the mobile network toward the handset.",
      icon: <CloudServerOutlined />,
    },
    {
      title: "Receipt returns",
      detail:
        "If requested, a DLR follows the inbound path back to the application.",
      icon: <CheckCircleFilled />,
    },
  ],
  mo: [
    {
      title: "Handset sends",
      detail:
        "The subscriber sends a reply or the network creates a delivery receipt.",
      icon: <MessageOutlined />,
    },
    {
      title: "SMSC forwards",
      detail:
        "The upstream SMSC sends a deliver_sm PDU over the receiver side of the bind.",
      icon: <CloudServerOutlined />,
    },
    {
      title: "Jasmin classifies",
      detail:
        "Message type, connector, address and tags become routing inputs.",
      icon: <SettingOutlined />,
    },
    {
      title: "MO route matches",
      detail:
        "Priority and filters choose the first valid downstream destination.",
      icon: <BranchesOutlined />,
    },
    {
      title: "Application receives",
      detail:
        "Traffic is delivered to an HTTP callback or a bound SMPP client.",
      icon: <GlobalOutlined />,
    },
  ],
  dlr: [
    {
      title: "Submit is answered",
      detail:
        "submit_sm_resp carries the SMSC message ID. At level 1 or 3 this alone produces a callback.",
      icon: <ApiOutlined />,
    },
    {
      title: "Correlation is stored",
      detail:
        "The SMSC ID is tied to your gateway message ID so a later receipt can find it.",
      icon: <LinkOutlined />,
    },
    {
      title: "Carrier reports",
      detail:
        "Minutes or hours later the network sends a receipt with a final state: DELIVRD, EXPIRED, UNDELIV and so on.",
      icon: <CloudServerOutlined />,
    },
    {
      title: "Receipt is matched",
      detail:
        "The receipt is resolved back to the original message, and the commercial record gains its delivery outcome.",
      icon: <AuditOutlined />,
    },
    {
      title: "Late money settles",
      detail:
        "On a split account the remaining share of the rate is applied once, idempotently.",
      icon: <WalletOutlined />,
    },
    {
      title: "Application is told",
      detail:
        "Your callback receives level=2 and must answer ACK/Jasmin, or the delivery is retried.",
      icon: <CheckCircleFilled />,
    },
  ],
};

const searchableText = (...values: Array<string | undefined>) =>
  values.join(" ").toLowerCase();

/**
 * A figure in the standalone gallery below still belongs to the lesson(s)
 * that explain it — this renders the "used in" chips that jump straight to
 * them, so the gallery never reads as disconnected from the lessons again.
 */
const FigureLessonLinks = ({
  ids,
  onOpen,
}: {
  ids: string[];
  onOpen: (lesson: Lesson) => void;
}) => {
  const matches = ids
    .map((id) => lessons.find((lesson) => lesson.id === id))
    .filter((lesson): lesson is Lesson => Boolean(lesson));

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
  const [completedLessonIds, setCompletedLessonIds] =
    useState<string[]>(readCompletedLessonIds);
  const normalizedQuery = query.trim().toLowerCase();

  useEffect(() => {
    try {
      window.localStorage.setItem(
        EDUCATION_PROGRESS_KEY,
        JSON.stringify(completedLessonIds),
      );
    } catch {
      // Completion remains usable for this session if storage is unavailable.
    }
  }, [completedLessonIds]);

  // A lesson about a screen this deployment hides would send the reader to a
  // 404. Interceptor management is opt-in because the scripts run as code on
  // the gateway host.
  const availableLessons = useMemo(
    () =>
      lessons.filter(
        (lesson) =>
          !lesson.requiresInterceptors || features.interceptor_editing,
      ),
    [features.interceptor_editing],
  );

  const completedLessons = useMemo(
    () => new Set(completedLessonIds),
    [completedLessonIds],
  );

  const trackSections = useMemo(
    () => buildTrackSections(track, availableLessons),
    [track, availableLessons],
  );

  const visibleLessons = useMemo(
    () => trackSections.flatMap((section) => section.lessons),
    [trackSections],
  );

  const lessonPositions = useMemo(() => {
    const positions = new Map<string, LessonPosition>();

    (["operate", "network"] as const).forEach((trackId) => {
      const sections = buildTrackSections(trackId, availableLessons);
      const sequence = sections.flatMap((section) => section.lessons);

      sections.forEach((section) => {
        section.lessons.forEach((lesson) => {
          const index = sequence.findIndex(
            (candidate) => candidate.id === lesson.id,
          );

          positions.set(lesson.id, {
            index,
            total: sequence.length,
            sectionTitle: section.title,
            previous: sequence[index - 1],
            next: sequence[index + 1],
          });
        });
      });
    });

    return positions;
  }, [availableLessons]);

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
        searchableText(
          item.category,
          item.question,
          item.keywords,
          String(item.answer),
        ).includes(normalizedQuery),
      ),
      glossary: glossary.filter((item) =>
        searchableText(
          item.term,
          item.expansion,
          item.definition,
        ).includes(normalizedQuery),
      ),
    };
  }, [normalizedQuery, availableLessons]);

  const operatorSections = useMemo(
    () => buildTrackSections("operate", availableLessons),
    [availableLessons],
  );

  const operatorLessons = useMemo(
    () => operatorSections.flatMap((section) => section.lessons),
    [operatorSections],
  );

  const operatorMinutes = operatorLessons.reduce(
    (total, lesson) => total + parseMinutes(lesson.duration),
    0,
  );

  const operatorCompletedCount = operatorLessons.filter((lesson) =>
    completedLessons.has(lesson.id),
  ).length;

  const operatorPercent =
    operatorLessons.length === 0
      ? 0
      : Math.round(
          (operatorCompletedCount / operatorLessons.length) * 100,
        );

  const operatorResumeLesson =
    operatorLessons.find(
      (lesson) => !completedLessons.has(lesson.id),
    ) ?? operatorLessons[0];

  const trackMinutes = visibleLessons.reduce(
    (total, lesson) => total + parseMinutes(lesson.duration),
    0,
  );

  const trackCompletedCount = visibleLessons.filter((lesson) =>
    completedLessons.has(lesson.id),
  ).length;

  const trackPercent =
    visibleLessons.length === 0
      ? 0
      : Math.round((trackCompletedCount / visibleLessons.length) * 100);

  const recommendedLesson =
    visibleLessons.find(
      (lesson) => !completedLessons.has(lesson.id),
    ) ?? visibleLessons[0];

  const openPosition = openLesson
    ? lessonPositions.get(openLesson.id)
    : undefined;

  const previousLesson = openPosition?.previous;
  const nextLesson = openPosition?.next;

  const openLessonComplete = openLesson
    ? completedLessons.has(openLesson.id)
    : false;

  const resultCount = searchResults
    ? searchResults.lessons.length +
      searchResults.faq.length +
      searchResults.glossary.length
    : 0;

  const openLessonDetail = (lesson: Lesson) => {
    setTrack(lesson.track);
    setOpenLesson(lesson);
  };

  const setLessonCompletion = (
    lessonId: string,
    complete: boolean,
  ) => {
    setCompletedLessonIds((previous) => {
      if (complete) {
        return previous.includes(lessonId)
          ? previous
          : [...previous, lessonId];
      }

      return previous.filter((id) => id !== lessonId);
    });
  };

  const resetTrackProgress = (trackId: TrackID) => {
    const lessonIds = new Set(
      buildTrackSections(trackId, availableLessons)
        .flatMap((section) => section.lessons)
        .map((lesson) => lesson.id),
    );

    setCompletedLessonIds((previous) =>
      previous.filter((id) => !lessonIds.has(id)),
    );
  };

  return (
    <div className="page-container education-page">
      <section className="education-hero">
        <div className="education-hero-copy">
          <PageTitle
            eyebrow="Education center"
            title="Operator learning paths"
            description="Follow an ordered curriculum for operating Jasmin or understanding the network beneath it. Lessons remain directly accessible when an incident requires a quick answer."
          />
          <label className="education-search">
            <span className="sr-only">
              Search lessons, FAQ and glossary
            </span>
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

        <aside
          className="education-resume-card"
          aria-label="Operate Jasmin progress"
        >
          <div className="education-resume-heading">
            <span
              className="education-resume-icon"
              aria-hidden="true"
            >
              <BookOutlined />
            </span>
            <div>
              <span>Recommended path</span>
              <strong>Operate Jasmin</strong>
            </div>
          </div>

          <div className="education-resume-lesson">
            <span>
              {operatorCompletedCount === operatorLessons.length &&
              operatorLessons.length > 0
                ? "Path complete"
                : operatorCompletedCount === 0
                  ? "Start here"
                  : "Continue here"}
            </span>
            <strong>
              {operatorCompletedCount === operatorLessons.length &&
              operatorLessons.length > 0
                ? "Review any lesson when needed"
                : operatorResumeLesson?.title ?? "No lessons available"}
            </strong>
          </div>

          <div className="education-progress-copy">
            <span>
              {operatorCompletedCount} of {operatorLessons.length} complete
            </span>
            <span>About {operatorMinutes} min</span>
          </div>

          <span
            className="education-progress-bar is-dark"
            role="progressbar"
            aria-label="Operate Jasmin path completion"
            aria-valuemin={0}
            aria-valuemax={operatorLessons.length}
            aria-valuenow={operatorCompletedCount}
          >
            <span style={{ width: `${operatorPercent}%` }} />
          </span>

          <Button
            type="primary"
            disabled={!operatorResumeLesson}
            onClick={() => {
              if (!operatorResumeLesson) return;
              setQuery("");
              openLessonDetail(operatorResumeLesson);
            }}
          >
            {operatorCompletedCount === 0
              ? "Start at step 1"
              : operatorCompletedCount === operatorLessons.length
                ? "Review from step 1"
                : "Continue path"}
          </Button>
        </aside>
      </section>

      {searchResults ? (
        <section
          className="education-search-results"
          aria-live="polite"
        >
          <div className="section-heading">
            <div>
              <span className="section-kicker">Search</span>
              <h2>
                {resultCount} result{resultCount === 1 ? "" : "s"} for
                “{query.trim()}”
              </h2>
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
              {searchResults.lessons.map((lesson) => {
                const position = lessonPositions.get(lesson.id);
                const complete = completedLessons.has(lesson.id);

                return (
                  <article
                    className="education-result-card"
                    key={lesson.id}
                  >
                    <div className="education-result-meta">
                      <Tag
                        color={
                          lesson.track === "operate" ? "cyan" : "blue"
                        }
                      >
                        {trackNames[lesson.track]}
                      </Tag>
                      <span className={complete ? "is-complete" : ""}>
                        {complete ? (
                          <CheckCircleFilled aria-hidden="true" />
                        ) : null}
                        {complete ? "Complete" : "Not complete"}
                      </span>
                    </div>
                    <div>
                      <h3>{lesson.title}</h3>
                      <p>{lesson.summary}</p>
                      {position ? (
                        <small>
                          Step {position.index + 1} of {position.total} ·{" "}
                          {position.sectionTitle}
                        </small>
                      ) : null}
                    </div>
                    <button
                      type="button"
                      className="education-text-link is-button"
                      onClick={() => openLessonDetail(lesson)}
                    >
                      Open lesson{" "}
                      <ArrowRightOutlined aria-hidden="true" />
                    </button>
                  </article>
                );
              })}

              {searchResults.faq.map((item) => (
                <article
                  className="education-result-card"
                  key={item.key}
                >
                  <Tag>
                    FAQ · {item.category}
                  </Tag>
                  <div>
                    <h3>{item.question}</h3>
                    <p>{item.answer}</p>
                  </div>
                </article>
              ))}

              {searchResults.glossary.map((item) => (
                <article
                  className="education-result-card"
                  key={item.term}
                >
                  <Tag>Glossary</Tag>
                  <div>
                    <h3>
                      {item.term} · {item.expansion}
                    </h3>
                    <p>{item.definition}</p>
                  </div>
                </article>
              ))}
            </div>
          )}
        </section>
      ) : (
        <>
          <section
            className="education-track-panel"
            id="learning-path"
            aria-labelledby="learning-path-heading"
          >
            <div className="section-heading education-track-heading">
              <div>
                <span className="section-kicker">
                  Guided curriculum
                </span>
                <h2 id="learning-path-heading">
                  Choose a learning path
                </h2>
                <p>
                  Follow the numbered sequence when onboarding. Every
                  lesson remains available out of order for incident
                  response.
                </p>
              </div>
              <Segmented
                size="large"
                value={track}
                aria-label="Learning path"
                onChange={(value) => setTrack(value as TrackID)}
                options={[
                  {
                    value: "operate",
                    label: "Operate Jasmin",
                    icon: <ControlOutlined />,
                  },
                  {
                    value: "network",
                    label: "Understand the network",
                    icon: <ThunderboltOutlined />,
                  },
                ]}
              />
            </div>

            <div className="education-track-progress">
              <div>
                <span>Path progress</span>
                <strong>
                  {trackCompletedCount} of {visibleLessons.length} lessons
                  complete
                </strong>
              </div>

              <div className="education-track-progress-meter">
                <span
                  className="education-progress-bar"
                  role="progressbar"
                  aria-label={`${trackNames[track]} path completion`}
                  aria-valuemin={0}
                  aria-valuemax={visibleLessons.length}
                  aria-valuenow={trackCompletedCount}
                >
                  <span style={{ width: `${trackPercent}%` }} />
                </span>
                <small>
                  {trackPercent}% · about {trackMinutes} min total
                </small>
              </div>

              <div className="education-track-actions">
                {trackCompletedCount > 0 ? (
                  <Popconfirm
                    title={`Reset ${trackNames[track]} progress?`}
                    description="This only clears completion markers stored in this browser."
                    okText="Reset progress"
                    cancelText="Keep progress"
                    onConfirm={() => resetTrackProgress(track)}
                  >
                    <Button type="text">Reset progress</Button>
                  </Popconfirm>
                ) : null}

                <Button
                  type="primary"
                  disabled={!recommendedLesson}
                  onClick={() => {
                    if (recommendedLesson) {
                      openLessonDetail(recommendedLesson);
                    }
                  }}
                >
                  {trackCompletedCount === 0
                    ? "Start path"
                    : trackCompletedCount === visibleLessons.length
                      ? "Review path"
                      : "Continue path"}
                </Button>
              </div>
            </div>

            <div className="education-curriculum">
              {trackSections.map((section, sectionIndex) => {
                const sectionCompleted = section.lessons.filter(
                  (lesson) => completedLessons.has(lesson.id),
                ).length;

                const sectionStart =
                  lessonPositions.get(section.lessons[0]?.id)?.index ?? 0;

                return (
                  <section
                    className="curriculum-section"
                    key={section.id}
                    aria-labelledby={`curriculum-${track}-${section.id}`}
                  >
                    <header className="curriculum-section-heading">
                      <span
                        className="curriculum-section-number"
                        aria-hidden="true"
                      >
                        {String(sectionIndex + 1).padStart(2, "0")}
                      </span>
                      <div>
                        <h3
                          id={`curriculum-${track}-${section.id}`}
                        >
                          {section.title}
                        </h3>
                        <p>{section.description}</p>
                      </div>
                      <span className="curriculum-section-meta">
                        {sectionCompleted}/{section.lessons.length}{" "}
                        complete · {section.minutes} min
                      </span>
                    </header>

                    <ol
                      className="curriculum-lessons"
                      start={sectionStart + 1}
                    >
                      {section.lessons.map((lesson) => {
                        const position = lessonPositions.get(lesson.id);
                        const complete = completedLessons.has(lesson.id);
                        const current =
                          recommendedLesson?.id === lesson.id;
                        const previous = position?.previous;
                        const step = (position?.index ?? 0) + 1;

                        return (
                          <li key={lesson.id} value={step}>
                            <button
                              type="button"
                              className={[
                                "curriculum-lesson-card",
                                complete ? "is-complete" : "",
                                current ? "is-current" : "",
                              ]
                                .filter(Boolean)
                                .join(" ")}
                              aria-current={
                                current ? "step" : undefined
                              }
                              aria-label={`Step ${step}: ${lesson.title}. ${
                                complete
                                  ? "Complete."
                                  : current
                                    ? "Next recommended lesson."
                                    : "Not complete."
                              }`}
                              onClick={() =>
                                openLessonDetail(lesson)
                              }
                            >
                              <span
                                className="curriculum-lesson-marker"
                                aria-hidden="true"
                              >
                                {complete ? (
                                  <CheckCircleFilled />
                                ) : (
                                  step
                                )}
                              </span>

                              <span
                                className="curriculum-lesson-icon"
                                aria-hidden="true"
                              >
                                {lesson.icon}
                              </span>

                              <span className="curriculum-lesson-main">
                                <span className="curriculum-lesson-meta">
                                  Step {step} of{" "}
                                  {position?.total ??
                                    visibleLessons.length}{" "}
                                  · {lesson.duration}
                                </span>
                                <span
                                  className="curriculum-lesson-title"
                                  role="heading"
                                  aria-level={4}
                                >
                                  {lesson.title}
                                </span>
                                <span className="curriculum-lesson-summary">
                                  {lesson.summary}
                                </span>
                                <span className="curriculum-lesson-dependency">
                                  {previous
                                    ? `Recommended after step ${
                                        step - 1
                                      }: ${previous.title}`
                                    : "Start here · no prerequisite"}
                                </span>
                              </span>

                              <span className="curriculum-lesson-outcome">
                                <small>Outcome</small>
                                <span>{lesson.outcome}</span>
                              </span>

                              <span
                                className={[
                                  "curriculum-lesson-status",
                                  complete
                                    ? "is-complete"
                                    : current
                                      ? "is-current"
                                      : "",
                                ]
                                  .filter(Boolean)
                                  .join(" ")}
                              >
                                {complete ? (
                                  <CheckCircleFilled
                                    aria-hidden="true"
                                  />
                                ) : null}
                                {complete
                                  ? "Complete"
                                  : current
                                    ? trackCompletedCount === 0
                                      ? "Start here"
                                      : "Continue here"
                                    : "Not complete"}
                              </span>

                              <ArrowRightOutlined
                                className="curriculum-lesson-arrow"
                                aria-hidden="true"
                              />
                            </button>
                          </li>
                        );
                      })}
                    </ol>
                  </section>
                );
              })}
            </div>
          </section>

          <section className="education-journey-panel">
            <div className="section-heading journey-heading">
              <div>
                <span className="section-kicker">
                  Architecture reference
                </span>
                <h2>Follow one message end to end</h2>
                <p>
                  Every screen in the console controls or observes one
                  step in this path.
                </p>
              </div>
              <Segmented
                value={journey}
                aria-label="Message journey"
                onChange={(value) =>
                  setJourney(value as JourneyID)
                }
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
                  <div
                    className="journey-icon"
                    aria-hidden="true"
                  >
                    {step.icon}
                  </div>
                  <span className="journey-order">
                    {index + 1}
                  </span>
                  <h3>{step.title}</h3>
                  <p>{step.detail}</p>
                  {index < journeys[journey].length - 1 ? (
                    <ArrowRightOutlined
                      className="journey-arrow"
                      aria-hidden="true"
                    />
                  ) : null}
                </li>
              ))}
            </ol>

            <div className="plane-explainer">
              <article>
                <ControlOutlined aria-hidden="true" />
                <div>
                  <strong>Control plane</strong>
                  <p>
                    Users, routes, filters, connectors and profiles
                    describe what the gateway should do.
                  </p>
                </div>
              </article>

              <article>
                <ThunderboltOutlined aria-hidden="true" />
                <div>
                  <strong>Data plane</strong>
                  <p>
                    Live HTTP and SMPP sessions authenticate, route,
                    charge and move each message.
                  </p>
                </div>
              </article>

              <div className="plane-note">
                <InfoCircleOutlined aria-hidden="true" />
                <span>
                  Desired configuration and observed runtime state can
                  differ. The console shows both so operators can diagnose
                  the gap.
                </span>
              </div>
            </div>
          </section>

          <section className="education-figure-panel">
            <div
              className="section-heading compact"
              style={{ padding: 0, border: "none" }}
            >
              <div>
                <span className="section-kicker">
                  Visual reference
                </span>
                <h2>The five things that are easier to see</h2>
                <p>
                  Each figure states one behaviour that regularly
                  surprises people — including a couple this gateway
                  inherited on purpose.
                </p>
              </div>
            </div>

            <div className="education-figure-grid">
              <article className="education-figure-card is-wide">
                <h3>What a bind actually is</h3>
                <p>
                  Which side may send PDUs — not who dialled the
                  connection.
                </p>
                <SMPPSessionDiagram />
                <FigureLessonLinks
                  ids={["smpp-roles", "binds", "smpps-binds"]}
                  onOpen={openLessonDetail}
                />
              </article>

              <article className="education-figure-card is-wide">
                <h3>Why a route you added never fires</h3>
                <p>
                  Evaluation stops at the first match, from the highest
                  order down.
                </p>
                <RoutePriorityDiagram />
                <FigureLessonLinks
                  ids={["mt-routing"]}
                  onOpen={openLessonDetail}
                />
              </article>

              <article className="education-figure-card">
                <h3>Why one text became three messages</h3>
                <p>Capacity per part, by encoding.</p>
                <SegmentationChart />
                <FigureLessonLinks
                  ids={["encoding", "charging-models"]}
                  onOpen={openLessonDetail}
                />
              </article>

              <article className="education-figure-card">
                <h3>When the money is taken</h3>
                <p>
                  One rate, split between submit time and acceptance.
                </p>
                <ChargingSplitChart />
                <FigureLessonLinks
                  ids={["billing-accounts", "charging-models"]}
                  onOpen={openLessonDetail}
                />
              </article>

              <article className="education-figure-card is-wide">
                <h3>
                  What each receipt level actually delivers
                </h3>
                <p>And why level 2 can leave you with silence.</p>
                <DLRLevelsDiagram />
                <FigureLessonLinks
                  ids={["receipts-in-practice", "directions"]}
                  onOpen={openLessonDetail}
                />
              </article>
            </div>
          </section>

          <section className="education-reference-grid">
            <div className="education-faq-panel">
              <div className="section-heading compact">
                <div>
                  <span className="section-kicker">
                    Common questions
                  </span>
                  <h2>FAQ for real operations</h2>
                </div>
                <QuestionCircleOutlined
                  className="section-heading-icon"
                  aria-hidden="true"
                />
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
                  children: (
                    <p className="faq-answer">{item.answer}</p>
                  ),
                }))}
              />
            </div>

            <aside className="education-glossary-panel">
              <div className="section-heading compact">
                <div>
                  <span className="section-kicker">
                    Keep nearby
                  </span>
                  <h2>Essential glossary</h2>
                </div>
                <FileSearchOutlined
                  className="section-heading-icon"
                  aria-hidden="true"
                />
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
        width={920}
        rootClassName="lesson-drawer"
        title={
          openLesson ? (
            <div className="lesson-drawer-title">
              <span>
                {trackNames[openLesson.track]}
                {openPosition
                  ? ` · Step ${openPosition.index + 1} of ${
                      openPosition.total
                    }`
                  : ""}
              </span>
              <strong>{openLesson.title}</strong>
            </div>
          ) : undefined
        }
        footer={
          openLesson ? (
            <div className="lesson-drawer-footer">
              <span aria-live="polite">
                {openLessonComplete
                  ? "Completion saved in this browser."
                  : "This lesson is not marked complete."}
              </span>

              <div>
                <Button
                  type={
                    openLessonComplete ? "default" : "primary"
                  }
                  onClick={() =>
                    setLessonCompletion(
                      openLesson.id,
                      !openLessonComplete,
                    )
                  }
                >
                  {openLessonComplete
                    ? "Mark as not complete"
                    : "Mark lesson complete"}
                </Button>

                <Button
                  type={
                    openLessonComplete ? "primary" : "default"
                  }
                  onClick={() => {
                    if (nextLesson) {
                      openLessonDetail(nextLesson);
                    } else {
                      setOpenLesson(null);
                    }
                  }}
                >
                  {nextLesson ? "Next lesson" : "Close lesson"}
                  {nextLesson ? (
                    <ArrowRightOutlined aria-hidden="true" />
                  ) : null}
                </Button>
              </div>
            </div>
          ) : null
        }
        destroyOnHidden
      >
        {openLesson ? (
          <div className="lesson-detail">
            <div className="lesson-detail-meta">
              <Tag
                color={
                  openLesson.track === "operate" ? "cyan" : "blue"
                }
              >
                {openPosition?.sectionTitle ??
                  trackNames[openLesson.track]}
              </Tag>
              <span>{openLesson.duration}</span>
              <span
                className={[
                  "lesson-completion-badge",
                  openLessonComplete ? "is-complete" : "",
                ]
                  .filter(Boolean)
                  .join(" ")}
              >
                {openLessonComplete ? (
                  <CheckCircleFilled aria-hidden="true" />
                ) : null}
                {openLessonComplete ? "Complete" : "Not complete"}
              </span>
            </div>

            {openPosition ? (
              <nav
                className="lesson-detail-sequence"
                aria-label="Learning path lesson navigation"
              >
                <button
                  type="button"
                  disabled={!previousLesson}
                  onClick={() => {
                    if (previousLesson) {
                      openLessonDetail(previousLesson);
                    }
                  }}
                >
                  <span>Previous</span>
                  <strong>
                    {previousLesson?.title ?? "Start of path"}
                  </strong>
                </button>

                <div>
                  <strong>
                    Step {openPosition.index + 1} of{" "}
                    {openPosition.total}
                  </strong>
                  <span>{openPosition.sectionTitle}</span>
                </div>

                <button
                  type="button"
                  disabled={!nextLesson}
                  onClick={() => {
                    if (nextLesson) {
                      openLessonDetail(nextLesson);
                    }
                  }}
                >
                  <span>Next</span>
                  <strong>
                    {nextLesson?.title ?? "End of path"}
                  </strong>
                </button>
              </nav>
            ) : null}

            <div className="lesson-detail-layout">
              <div className="lesson-detail-columns">
                <div className="lesson-detail-main">
                  <p className="lesson-detail-lead">
                    {openLesson.why}
                  </p>

                  {openLesson.figure ? (
                    <div className="lesson-detail-figures">
                      {openLesson.figure}
                    </div>
                  ) : null}

                  <span className="lesson-detail-subhead">
                    How it works
                  </span>

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
                    <div>
                      <small>Lesson outcome</small>
                      <span>{openLesson.outcome}</span>
                    </div>
                  </div>

                  <Alert
                    type="warning"
                    showIcon
                    message="Watch out"
                    description={openLesson.watchOut}
                  />

                  <div className="lesson-detail-prerequisite">
                    <h4>Recommended prerequisite</h4>
                    {previousLesson ? (
                      <button
                        type="button"
                        onClick={() =>
                          openLessonDetail(previousLesson)
                        }
                      >
                        <span>
                          Step {openPosition?.index}:{" "}
                          {previousLesson.title}
                        </span>
                        <ArrowRightOutlined aria-hidden="true" />
                      </button>
                    ) : (
                      <p>None. This is the start of the path.</p>
                    )}
                    <small>
                      Lessons are never locked; open any lesson during
                      an incident.
                    </small>
                  </div>

                  {openLesson.href ? (
                    <Link
                      to={openLesson.href}
                      className="lesson-detail-action-link"
                      onClick={() => setOpenLesson(null)}
                    >
                      <span>{openLesson.action}</span>
                      <ArrowRightOutlined aria-hidden="true" />
                    </Link>
                  ) : null}

                  <RelatedLessons
                    ids={openLesson.related}
                    available={availableLessons}
                    onOpen={openLessonDetail}
                  />
                </aside>
              </div>
            </div>
          </div>
        ) : null}
      </Drawer>
    </div>
  );
};
