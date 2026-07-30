# Idea: Trubadur

> Discussion note — conceptual only. This document does not imply an
> implementation commitment.

## Core idea

Trubadur would be a subsystem for creating, configuring, observing, and
controlling Synevyr deployments. It could generate a live topology of nodes and
traffic flows, explain how messages move through the platform, and coordinate
safe operational changes.

The clean architectural division would be:

- **Synevyr is the data plane:** it accepts, routes, bills, queues, and delivers
  messages.
- **Trubadur is the control and intelligence plane:** it discovers, configures,
  observes, explains, and coordinates Synevyr deployments.

Trubadur should probably be a separately deployable subsystem rather than just
another package inside the gateway. A Git submodule would only make sense if it
has a genuinely independent repository and release lifecycle.

The most important boundary is that Synevyr must continue processing traffic
when Trubadur is unavailable. Trubadur must not sit synchronously in the message
path.

## What a node means

Trubadur could present two related topologies.

### Infrastructure topology

This view represents:

- Synevyr gateway instances
- Active/passive pairs
- PostgreSQL
- RabbitMQ
- Redis
- Load balancers
- Hosts, regions, and availability zones
- External services and network relationships

### Messaging topology

This view represents:

- HTTP senders
- Customer ESMEs
- MT and MO routing tables
- Filters and interceptors
- SMPP connectors
- Carrier SMSCs
- Queues
- MO webhooks
- DLR destinations
- Billing entities

For example:

```text
Customer A
   ↓ HTTP/SMPP
Synevyr Cell Kyiv
   ↓ route: Ukraine Mobile
SMSC Connector A ──70%──→ Carrier A
SMSC Connector B ──30%──→ Carrier B
```

An edge could show throughput, success rate, latency, queue depth, throttling,
billing cost, retry count, and recent failures.

## Synevyr cells

Synevyr currently supports an active/passive model and explicitly does not
support multi-active operation. Trubadur should respect this boundary.

A useful abstraction is a **Synevyr cell**:

```text
Trubadur
   ├── Cell A: active gateway + standby + stores + connectors
   ├── Cell B: active gateway + standby + stores + connectors
   └── Cell C: active gateway + standby + stores + connectors
```

This allows horizontal growth without immediately requiring a multi-active
gateway. Cells could be divided by customer, geography, carrier, regulatory
boundary, or traffic class. Trubadur would understand which instance is active
inside each cell and coordinate at the cell level.

## Core operational capabilities

### Inventory and desired state

- Discover nodes, roles, versions, capabilities, and health.
- Record the active/passive state of each cell.
- Show the configuration revision running on each node.
- Declare the desired state of a cell.
- Detect configuration drift.
- Validate compatibility between Trubadur, Synevyr, and storage versions.

### Topology and traffic

- Visualize MT, MO, and DLR paths separately.
- Overlay live traffic, errors, latency, and capacity on topology edges.
- Identify unused connectors, unreachable routes, and missing fallbacks.
- Save and compare topology snapshots.
- Generate architecture and dependency documentation from live state.

### Safe configuration control

- Validate changes before applying them.
- Preview configuration differences.
- Show the customers and traffic paths affected by a proposed change.
- Apply changes gradually.
- Verify health and traffic after a change.
- Roll back to a known-good configuration.
- Keep a complete audit history.

### Fleet operations

- Bootstrap new cells.
- Coordinate upgrades and migrations.
- Drain traffic before maintenance.
- Disable or isolate unhealthy connectors.
- Coordinate active/passive promotion without fighting Synevyr's PostgreSQL
  fencing.
- Verify backups and disaster-recovery readiness.

## Broader uses

Trubadur can be valuable even for a single-node Synevyr deployment.

### Message journey explorer

Given a message ID, Trubadur could tell its complete story:

```text
Accepted through HTTP
→ authenticated as Customer A
→ interceptor modified sender ID
→ route 17 matched
→ charged $0.012
→ queued for connector vodafone-ua
→ delayed for 4.2 seconds by throttling
→ accepted by SMSC as ID 782991
→ DLR received: DELIVRD
→ callback delivered successfully
```

This would be especially valuable for support and incident investigation. It
also fits the name “Trubadur”: it narrates the journey of a message.

### Routing laboratory and digital twin

Operators could ask:

- Where would this message go?
- Which filter matched, and why?
- Why was another connector not selected?
- What happens if a carrier is unavailable?
- Would a proposed route change increase cost?
- How would historical traffic behave with a new topology?
- Is a route unreachable, shadowed, conflicting, or missing a fallback?

Historical message metadata could be replayed through a proposed topology
without sending messages, changing balances, or exposing message content.

### Incident command center

Trubadur could correlate signals from different components and provide an
explanation such as:

> Delivery rate dropped because Carrier B began throttling. Its queue is
> growing, and no fallback route exists for 38% of the affected traffic.

It could produce:

- Incident timelines
- Affected customers and destinations
- Message counts
- Likely root causes
- Suggested operator actions
- Recovery verification

### Customer support

Support staff should be able to investigate messages without direct access to
RabbitMQ, databases, or production servers. Trubadur could answer:

- Why is this message pending?
- Was the customer charged?
- Was it rejected by Synevyr or by the carrier?
- Was a DLR received?
- Did the customer's callback fail?
- Are other messages from this sender affected?
- Can the message safely be retried?

Sensitive message content could remain hidden while operational metadata is
available.

### Carrier quality and cost management

Trubadur could compare carriers and connectors by:

- Delivery rate
- DLR latency
- Submission latency
- Throttling frequency
- Error distribution
- Destination network
- Time of day
- Price per submitted message
- Price per successfully delivered message
- Actual versus contracted throughput

This could support quality-aware least-cost routing. The cheapest submission is
not necessarily the cheapest successful delivery.

### Fraud and anomaly detection

Potential signals include:

- Sudden traffic spikes from one account
- Credential compromise
- Destination pumping
- Unexpected countries or prefixes
- Excessive retry patterns
- Sender-ID abuse
- Spam-like traffic distribution
- Unexpected routing or billing changes
- Connector behavior outside its normal profile

Initially, Trubadur should recommend or alert. Automatic blocking should be
introduced cautiously because a false positive can interrupt legitimate
traffic.

### Customer-facing SLA portal

A restricted customer view could provide:

- The customer's traffic and delivery statistics
- Current service health
- Message investigation
- Throughput and quota usage
- Billing and cost summaries
- Scheduled maintenance
- SLA reports
- Callback and SMPP bind status

Customers would see only their logical traffic, not private carrier or
cross-customer topology.

### Migration assistant

Trubadur could assist migration from Jasmin or another SMS gateway:

- Import and visualize existing configuration.
- Compare old and new routing behavior.
- Detect incompatible behavior.
- Replay historical traffic metadata.
- Run systems in shadow mode.
- Produce migration-readiness reports.
- Coordinate gradual customer or carrier cutover.

### Testing and certification

Trubadur could coordinate a telecom integration laboratory:

- Test new SMSC connections.
- Verify bind and reconnect behavior.
- Inject throttling, delays, and disconnects.
- Test unusual or malformed DLRs.
- Measure supported throughput.
- Validate encodings, long messages, TLVs, and sender IDs.
- Verify customer ESME behavior.
- Generate carrier or customer certification reports.

### Disaster recovery

Trubadur could continuously verify:

- Whether backups are recent.
- Whether backups can actually be restored.
- Whether standby configuration is compatible.
- Whether required secrets and certificates are available.
- Expected recovery time.
- Which traffic would be affected by a failure.
- Whether a cell can be recreated in another region.

### Compliance and governance

- Complete configuration audit logs
- Approval workflows for sensitive changes
- Role separation
- Data-residency enforcement
- Secret and certificate expiration tracking
- Retention policies
- Evidence of access to message metadata
- Detection of traffic crossing prohibited regions or carriers

### Capacity and commercial planning

- Forecast connector saturation.
- Predict queue growth and SMPP window pressure.
- Identify where additional carrier capacity is needed.
- Compare contracted throughput with actual usage.
- Estimate cost changes from routing modifications.
- Support carrier contract and capacity negotiations with real evidence.

## Main risks

Trubadur could easily expand into a very large orchestration platform. Important
risks include:

- Holding every SMPP, database, and infrastructure secret centrally.
- Distributing one bad configuration to the entire fleet.
- Automatic failover conflicting with Synevyr's fencing.
- Displaying stale topology as if it were authoritative.
- High-cardinality per-message metrics overwhelming observability storage.
- Creating control loops that repeatedly move traffic between connectors.
- Coupling Synevyr startup or message handling to Trubadur.
- Trying to solve provisioning, monitoring, deployment, routing, analytics,
  security, and incident response in the first release.

Trubadur should use asynchronous desired-state coordination. Each Synevyr cell
must remain locally safe and operational when disconnected from Trubadur.

## Possible evolution

1. Read-only discovery and topology.
2. Health and traffic overlays.
3. Message journey exploration.
4. Route simulation and impact analysis.
5. Audited configuration management.
6. Node lifecycle, upgrades, and failover coordination.
7. Capacity, cost, anomaly detection, and controlled automation.

## Product direction

The topology map is a useful entry point, but it should not be the whole
product. A map that only draws boxes and arrows may become decorative.

The strongest potential capabilities are:

1. Tell the complete story of any message.
2. Simulate and explain routing before changing production.
3. Correlate failures across customers, routes, queues, and carriers.

Under this definition, Trubadur is not merely a node manager. It is the
**operational intelligence and orchestration layer for Synevyr**.
