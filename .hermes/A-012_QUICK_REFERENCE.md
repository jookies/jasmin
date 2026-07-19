# A-012 Quick Reference: AMQP Reconnection Contract

## What Gets Redeclared on Reconnect?

### ✅ YES - Topology Elements (Redeclared)
- **Exchanges**: `exchange_declare(exchange='messaging', type='topic')`
- **Queues**: `named_queue_declare(queue='queueName')`
- **Bindings**: `queue_bind(queue=..., exchange=..., routing_key=...)`

**How it works:**
1. Connection lost → factory reconnects (10 sec delay default)
2. New channel opens → `channelReady` deferred fires
3. Waiting components wake up → redeclare topology
4. AMQP operations are idempotent (no error if already exists)

### ❌ NO - Consumer Recovery (NOT Redeclared)
- **Consumer registrations**: `basic_consume(queue=..., consumer_tag=...)`

**Why it's a problem:**
1. Consumer setup only happens once in `addAmqpBroker()`
2. On reconnect, `addAmqpBroker()` is NOT called again
3. Consumers are lost and NOT automatically restored
4. Messages sit in queue but are never consumed

---

## In-Flight Message Handling

### Unacknowledged Messages
- All consumers use `no_ack=False` (manual ACK required)
- Connection loss before ACK → message stays in broker queue
- Redelivery? Only if consumer is re-established (which legacy doesn't do automatically)

### Explicit Rejection
```python
# Message can be rejected and requeued
yield amqpBroker.chan.basic_reject(delivery_tag=tag, requeue=1)
```

---

## Configuration

**Default reconnection behavior:**
```
reconnectOnConnectionLoss = True
reconnectOnConnectionFailure = True
reconnectOnConnectionLossDelay = 10 seconds
reconnectOnConnectionFailureDelay = 10 seconds
heartbeat = 0 (DISABLED)
```

---

## Code Patterns

### Topology Redeclaration (Used by Router, Throwers, Managers)
```python
@defer.inlineCallbacks
def addAmqpBroker(self, amqpBroker):
    # Wait for channel ready (fires on each reconnect)
    yield amqpBroker.channelReady
    
    # Redeclare topology
    yield amqpBroker.chan.exchange_declare(exchange='messaging', type='topic')
    yield amqpBroker.named_queue_declare(queue='myqueue')
    yield amqpBroker.chan.queue_bind(queue='myqueue', exchange='messaging', routing_key='*.#')
    
    # Start consumer (NOT recovered on reconnect)
    yield amqpBroker.chan.basic_consume(queue='myqueue', no_ack=False, consumer_tag='my-consumer')
    q = yield amqpBroker.client.queue('my-consumer')
    q.get().addCallback(self.on_message)
```

### Named Queue Declaration (Prevents duplicates within channel)
```python
# Only calls queue_declare if not already in self.queues
yield amqpBroker.named_queue_declare(queue='queueName')
# Note: self.queues is reset on every reconnect, so topology IS redeclared
```

---

## Factory State Transitions

```
Initial state:
  connected = False
  channelReady = None (Deferred, unfired)

On successful connection:
  connected = True
  channelReady fires (Deferred.callback())

On connection loss/failure:
  connected = False
  client = None
  channelReady = None (reset to new Deferred)
  
Reconnect triggered:
  preConnect() → create new channelReady Deferred
  TCP reconnect → new channel opened
  channelReady fires again (components redeclare topology)
```

---

## Critical Code Files

| File | Lines | Purpose |
|------|-------|---------|
| `jasmin/queues/factory.py` | 44-64 | `preConnect()` - resets channelReady |
| `jasmin/queues/factory.py` | 84-118 | Reconnection triggers |
| `jasmin/queues/factory.py` | 119-125 | `reConnect()` - initiates reconnect |
| `jasmin/queues/factory.py` | 175-181 | `_channel_open()` - fires channelReady |
| `jasmin/routing/router.py` | 76-104 | RouterPB topology setup (one-time) |
| `jasmin/routing/throwers.py` | 161-183 | Thrower topology setup (one-time) |
| `jasmin/managers/dlr.py` | 63-72 | DLR topology setup (one-time) |

---

## For Go Rewrite (A-012 Requirements)

**Must replicate:**
- ✅ Topology redeclaration on every reconnect
- ✅ Channel readiness signaling
- ✅ Unack'd message retention

**Should improve:**
- ❌ Auto-recover consumers on reconnect (legacy doesn't)
- ❌ Enable heartbeat by default (legacy has it off)
- ❌ Track in-flight messages explicitly

---

**Discovery Date**: 2026-07-19  
**Analyst**: Legacy Jasmin Source Code Review  
**Status**: Inventory Complete
