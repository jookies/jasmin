from twisted.internet import defer, reactor


@defer.inlineCallbacks
def slow_down(seconds):
    # Block on waitDeferred for 'seconds'
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred
