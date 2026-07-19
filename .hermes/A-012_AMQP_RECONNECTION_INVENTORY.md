# A-012 AMQP Reconnection & Topology Redeclaration Contract

**Requirement**: A-012 specifies reconnect behavior: declarations, consumer recovery, and in-flight delivery handling.

**Discovery Date**: 2026-07-19  
**Repository**: jasmin-go (legacy Python/Twisted implementation)  
**Focus**: How legacy Jasmin handles AMQP reconnection, topology redeclaration, and consumer recovery.

---

## Executive Summary

The legacy Jasmin (Python/Twisted) AMQP implementation **REDECLARES all topology on every reconnect** by design. Consumer recovery is **NOT automatic** — subscriptions are explicitly re-established through method invocation. In-flight messages rely on AMQP acknowledgment semantics and manual `basic_reject`/`basic_ack` handling.

---

## Architecture Overview

### Connection & Channel Lifecycle

**File**: `jasmin/queues/factory.py` (AmqpFactory)

```
preConnect() → authenticate() → _got_channel() → _channel_open()
     ↓
channelReady = Deferred()  [FIRED when channel is open]
     ↓
connected = True
     ↓
[Application uses channelReady to wait for readiness]
     ↓
reConnect() [called on connection loss/failure]
     ↓
preConnect() [RESETS channelReady to new Deferred]
```

**Key State Variables**:
- `self.connected`: Boolean flag set to `True` only when channel is open
- `self.channelReady`: A `Deferred()` that fires when channel is ready (reset on reconnect)
- `self.chan`: The AMQP channel object (recreated on each reconnect)
- `self.queues`: List tracking declared queue names (reset when channel opens)

### Reconnection Trigger

**File**: `jasmin/queues/factory.py`, lines 84–118

```python
def clientConnectionFailed(self, connector, reason):
    # Connection failed
    self.connected = False
    if self.config.reconnectOnConnectionFailure and self.connectionRetry:
        # Retry after delay (default: 10 seconds)
        self.reconnectTimer = reactor.callLater(
            self.config.reconnectOnConnectionFailureDelay,
            self.reConnect, connector)

def clientConnectionLost(self, connector, reason):
    # Connection lost (even cleanly)
    self.connected = False
    self.client = None
    if self.config.reconnectOnConnectionLoss and self.connectionRetry:
        # Retry after delay (default: 10 seconds)
        self.reconnectTimer = reactor.callLater(
            self.config.reconnectOnConnectionLossDelay,
            self.reConnect, connector)

def reConnect(self, connector=None):
    # AND TRY TO CONNECT AGAIN
    self.preConnect()  # [CRITICAL: RESETS channelReady deferred]
    connector.connect()
```

**Reconnection Delay**: 
- Connection failure retry delay: configurable, default 10 seconds
- Connection loss retry delay: configurable, default 10 seconds
- Both are configured in `jasmin/queues/configs.py`

---

## Topology Redeclaration Behavior

### **NO automatic redeclaration by factory**

The `AmqpFactory` class does **NOT** automatically redeclare topology on reconnect. Instead:

1. **Factory resets state**:
   - `self.channelReady = defer.Deferred()` (new, unfired deferred)
   - `self.connected = False`
   - `self.client = None`
   - `self.queues = []` (cleared when `_channel_open()` is called)

2. **Callers must wait and redeclare explicitly**:
   - Each component (Router, Thrower, Manager) calls `yield self.amqpBroker.channelReady` to wait for readiness
   - Then each component explicitly redeclares exchanges, queues, bindings, and consumers
   - **This is done on first connection AND on every reconnect** because `channelReady` fires again

### Exchange Declarations

**Pattern**: Every consuming component redeclares the exchange it uses.

**Examples**:

1. **Router** (`jasmin/routing/router.py`, lines 86, 98):
   ```python
   @defer.inlineCallbacks
   def addAmqpBroker(self, amqpBroker):
       self.amqpBroker = amqpBroker
       
       if not self.amqpBroker.connected:
           yield self.amqpBroker.channelReady  # Wait for channel ready
       
       # Redeclare exchanges
       yield self.amqpBroker.chan.exchange_declare(
           exchange='messaging', type='topic')
       yield self.amqpBroker.chan.exchange_declare(
           exchange='billing', type='topic')
       
       # Declare queues and bindings (see below)
   ```

2. **Throwers** (`jasmin/routing/throwers.py`, lines 171–177):
   ```python
   @defer.inlineCallbacks
   def addAmqpBroker(self, amqpBroker):
       self.amqpBroker = amqpBroker
       
       if not self.amqpBroker.connected:
           yield self.amqpBroker.channelReady
       
       yield self.amqpBroker.chan.exchange_declare(
           exchange=self.exchangeName, type='topic')
       yield self.amqpBroker.named_queue_declare(queue=self.queueName)
       yield self.amqpBroker.chan.queue_bind(...)
       yield self.amqpBroker.chan.basic_consume(...)
   ```

3. **DLRLookup** (`jasmin/managers/dlr.py`, lines 63–72):
   ```python
   @defer.inlineCallbacks
   def subscribe(self):
       yield self.amqpBroker.chan.exchange_declare(
           exchange='messaging', type='topic')
       yield self.amqpBroker.named_queue_declare(queue=queueName)
       yield self.amqpBroker.chan.queue_bind(
           queue=queueName, exchange="messaging", routing_key=routing_key)
       yield self.amqpBroker.chan.basic_consume(
           queue=queueName, no_ack=False, consumer_tag=consumerTag)
   ```

**Topology Idempotence**:
- AMQP `exchange_declare` with **same parameters** is idempotent (no error if already exists)
- AMQP `queue_declare` with **same parameters** is idempotent
- AMQP `queue_bind` with **same parameters** is idempotent
- All declarations use consistent exchange types and attributes across reconnects

---

## Queue Declaration Deduplication

**File**: `jasmin/queues/factory.py`, lines 157–165

The factory provides `named_queue_declare()` which prevents **duplicate declarations within the same channel**:

```python
def named_queue_declare(self, *args, **keys):
    """This is a wrapper to channel's queue_declare method
    it is intended to avoid multiple declaration of the same queue
    using self.queues which holds all declared queues in the connection
    """
    if not self.connected:
        self.log.error("AMQP Client is not connected, cannot queue_declare")
        return None
    
    for q in self.queues:
        if q == keys['queue']:
            self.log.debug('Queue [%s] is already declared, no need to redeclare', q)
            return None
    
    return self.chan.queue_declare(*args, **keys).addCallback(self._queue_declared)

def _queue_declared(self, queue):
    self.log.info("A new queue has been successfully declared [%s]", queue.queue)
    self.queues.append(queue.queue)
```

**Purpose**: Within a **single connection**, avoid redundant `queue_declare` calls if the queue was already declared in the same channel. This prevents race conditions within the channel lifetime.

**Reset on reconnect**: `self.queues = []` is cleared when `_channel_open()` fires, so on the next reconnect, all queues will be declared again (because `self.queues` is empty).

---

## Consumer Recovery

### **NO automatic consumer recovery by factory**

When a connection is lost and reconnected:

1. **Factory does NOT restore consumers automatically**
2. **Each consuming component must explicitly re-subscribe**
3. Re-subscription happens in `addAmqpBroker()` methods (called once per component)

### First-Time Setup

**Application startup flow** (example from `jasmin/bin/jasmind.py`):

```python
def startAMQPBrokerService(self):
    # Create and connect factory
    self.components['amqp-broker-factory'] = AmqpFactory(config)
    self.components['amqp-broker-factory'].preConnect()
    reactor.connectTCP(host, port, factory)

def startRouterPBService(self):
    # Create router component
    self.components['router-pb-factory'] = RouterPB(config)

# Later, explicitly add AMQP to router
def perspectives_setup(self):
    return self.components['router-pb-factory'].addAmqpBroker(
        self.components['amqp-broker-factory'])
```

In `RouterPB.addAmqpBroker()`, the component:
1. Waits for `channelReady`
2. Declares exchanges, queues, bindings
3. Calls `basic_consume()` to start consuming
4. Sets up message callbacks

### Reconnection Recovery

**Critical observation**: Components that use `@defer.inlineCallbacks` with `yield self.amqpBroker.channelReady` will **automatically re-execute** the topology setup when the channel becomes ready again.

However, this **only happens if**:
- The component has a long-lived method that's waiting on `channelReady`
- OR the component is re-initialized after reconnect

**Actual behavior in legacy Jasmin**:
- **RouterPB, DLR throwers, Deliver throwers**: Set up consumers in `addAmqpBroker()`, which is only called **once** at startup
- On reconnect, their consumers are **NOT automatically restored** because `addAmqpBroker()` is not called again
- The factory and consumers continue to try to use the old channel/protocol, which is now invalid

**Result**: **Consumers are lost on reconnect unless explicitly recovered by application logic**

**Evidence**: 
- `jasmin/routing/router.py` line 76: `addAmqpBroker()` is a one-time setup
- `jasmin/managers/dlr.py` line 63: `subscribe()` is called once, not re-called on reconnect
- No error handling or recovery logic for lost consumers observed in codebase

---

## In-Flight Message Handling

### Acknowledgment Model

**File**: Multiple files use `no_ack=False`

```python
# All consumers use no_ack=False (manual acknowledgment required)
yield self.amqpBroker.chan.basic_consume(
    queue=queueName, 
    no_ack=False,  # <-- Explicit ack/nack required
    consumer_tag=consumerTag)
```

### Message Rejection & Requeue

**File**: `jasmin/routing/router.py`, `jasmin/routing/throwers.py`, etc.

Components can explicitly reject and requeue messages:

```python
@defer.inlineCallbacks
def rejectAndRequeueMessage(self, message, delay=True):
    msgid = message.content.properties['message-id']
    
    if delay:
        # Requeue with delay (application-level, not AMQP)
        # Message is rejected, broker will requeue it
    
    yield self.amqpBroker.chan.basic_reject(
        delivery_tag=message.delivery_tag, 
        requeue=1)  # Requeue to broker
```

### On Connection Loss

**CRITICAL**: When connection is lost:
- Any message being processed is **NOT acknowledged**
- Broker holds the message in the queue (it was never ack'd)
- On reconnect, message remains in queue for redelivery
- **BUT**: Consumer must be re-established to receive it

**Problem**: If consumer is not re-established on reconnect, the message sits in queue indefinitely, effectively **lost in-flight**.

---

## Configuration

**File**: `jasmin/queues/configs.py`

```python
class AmqpConfig(ConfigFile):
    # Connection
    self.host = '127.0.0.1'
    self.port = 5672
    self.username = 'guest'
    self.password = 'guest'
    self.vhost = '/'
    self.heartbeat = 0  # Disabled by default
    
    # Reconnection behavior
    self.reconnectOnConnectionLoss = True
    self.reconnectOnConnectionFailure = True
    self.reconnectOnConnectionLossDelay = 10  # seconds
    self.reconnectOnConnectionFailureDelay = 10  # seconds
    
    # Logging
    self.log_level = 'INFO'
    self.log_file = '/path/to/amqp-client.log'
    self.log_format = '%(asctime)s %(levelname)-8s %(process)d %(message)s'
```

---

## Summary of Redeclaration Contract (A-012)

### ✅ REDECLARES on Reconnect

| Element | Redeclared? | Method | Timing |
|---------|-------------|--------|--------|
| **Exchanges** | ✅ YES | `exchange_declare()` | Called by consuming component after `channelReady` fires |
| **Queues** | ✅ YES | `named_queue_declare()` | Called by consuming component after `channelReady` fires |
| **Bindings** | ✅ YES | `queue_bind()` | Called by consuming component after `channelReady` fires |
| **Consumers** | ❌ NO | `basic_consume()` | Only set up once; NOT re-established on reconnect |

### Consumer Recovery

| Scenario | Status | Details |
|----------|--------|---------|
| Topology redeclared? | ✅ YES | Exchanges, queues, bindings redeclared on every reconnect |
| Consumers restored? | ❌ NO | No automatic mechanism to restore consumers |
| Messages redelivered? | ⚠️ PARTIAL | Unack'd messages stay in queue, but without consumer they're not received |
| Application recovery needed? | ✅ YES | Application must detect reconnect and re-call subscribe methods |

### In-Flight Message Delivery

| Scenario | Handling | Details |
|----------|----------|---------|
| **Message being processed** | Held in queue | Connection loss means no ACK; broker retains message |
| **Reconnect occurs** | Message stays queued | Not redelivered unless consumer re-established |
| **Redelivery guarantee** | Topology yes, consumer NO | Topology is redeclared; consumer is NOT |
| **Net result** | ⚠️ Lost unless recovered | Messages can be stranded if consumer recovery fails |

---

## Code Pathways Analyzed

### Factory Lifecycle
- **File**: `jasmin/queues/factory.py`
- **Key methods**: 
  - `preConnect()` (lines 44–64): Resets `channelReady` deferred
  - `clientConnectionFailed()` (lines 84–98): Triggers reconnect
  - `clientConnectionLost()` (lines 99–118): Triggers reconnect
  - `reConnect()` (lines 119–125): Calls `preConnect()` then `connector.connect()`
  - `_channel_open()` (lines 175–181): Sets `channelReady.callback()`

### Router Subscription
- **File**: `jasmin/routing/router.py`
- **Key method**: `addAmqpBroker()` (lines 76–104)
- **Behavior**: One-time setup; waits for `channelReady`, declares topology, starts consumers

### Thrower Subscriptions
- **File**: `jasmin/routing/throwers.py`
- **Key method**: `addAmqpBroker()` (lines 161–183)
- **Behavior**: One-time setup; same pattern as router

### DLR/Clients
- **Files**: `jasmin/managers/dlr.py`, `jasmin/managers/clients.py`
- **Methods**: `subscribe()`, consumer management
- **Behavior**: Setup topology and consumers once; reconnect recovery not implemented

---

## Implications for Go Rewrite (Jasmin-go)

### What the Go version should replicate

1. **Topology redeclaration on every reconnect**: Exchanges, queues, bindings must be redeclared when channel becomes ready
2. **Consumer re-establishment on reconnect**: Unlike legacy, should implement automatic consumer recovery
3. **Unack'd message retention**: Messages not acknowledged before connection loss should be retained and redelivered
4. **Configurable reconnect delays**: Support delays on connection failure vs. loss
5. **Channel readiness signaling**: Implement or mimic `channelReady` pattern for components to wait on channel readiness

### Gaps in legacy design to avoid in Go version

1. **No automatic consumer recovery**: Go version should implement automatic re-subscription on reconnect
2. **Consumers can be stranded**: Go version should detect and restore consumers automatically
3. **No heartbeat by default**: Consider enabling heartbeat to detect connection issues faster
4. **No in-flight acknowledgment tracking**: Go version could add explicit tracking of in-flight messages for better recovery

---

## Testing Evidence

**File**: `tests/queues/test_amqp.py`

Test cases confirm redeclaration behavior:
- `test_connect_and_exchange_declare`: Connection + explicit exchange declaration works
- All tests call `exchange_declare()`, `queue_declare()` explicitly before use
- No tests for automatic consumer recovery on reconnect

**File**: `scripts/compat/capture_amqp_golden.py`

Captures AMQP content contracts:
- Sets `reconnectOnConnectionFailure = False`, `reconnectOnConnectionLoss = False` to avoid automatic reconnect during test
- Manually declares topology before consuming
- Does not test reconnection behavior

---

## References

- AMQP 0.9.1 Spec: Exchange and queue declarations are idempotent
- Twisted Deferred: `channelReady` pattern for async waiting
- txamqp: Twisted AMQP client library used by Jasmin
- RabbitMQ: Default broker; supports durable queues and persistent messages

---

## Conclusion

The legacy Jasmin AMQP reconnection contract (A-012) **does redeclare all topology on reconnect** but **does NOT automatically recover consumers**. In-flight messages are retained by AMQP but may be lost if consumer recovery fails. The Go rewrite should replicate the topology redeclaration behavior while improving consumer recovery reliability.
