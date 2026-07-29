#!/usr/bin/python3
"""Trusted legacy PB listeners for the Go gateway.

The process must be placed on an internal network. It terminates PB/jelly and
pickle, then sends only authenticated normalized JSON to Go.
"""

import argparse
import logging
import os

from twisted.cred import portal
from twisted.cred.checkers import InMemoryUsernamePasswordDatabaseDontUse
from twisted.internet import reactor, ssl
from twisted.spread import pb

from jasmin.pbfacade.avatars import (
    ClientManagerAvatar,
    InterceptorAvatar,
    RouterAvatar,
    SMPPServerAvatar,
)
from jasmin.pbfacade.client import GoFacadeClient
from jasmin.tools.cred.portal import JasminPBRealm
from jasmin.tools.spread.pb import JasminPBPortalRoot


SERVICES = (
    ("router", RouterAvatar, "0.0.0.0", 8988, "radmin", "82a606ca5a0deea2b5777756788af5c8"),
    ("client", ClientManagerAvatar, "0.0.0.0", 8989, "cmadmin", "e1c5136acafb7016bc965597c992eb82"),
    ("smpps", SMPPServerAvatar, "0.0.0.0", 14000, "smppsadmin", "e97ab122faa16beea8682d84f3d2eea4"),
    ("interceptor", InterceptorAvatar, "0.0.0.0", 8987, "iadmin", "dd8b84cdb60655fed3b9b2d668c5bd9e"),
)


def options():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go-url", default=os.getenv("JASMIN_PB_FACADE_URL", "http://127.0.0.1:8998"))
    parser.add_argument("--token-env", default="JASMIN_PB_FACADE_TOKEN")
    parser.add_argument("--tls-cert")
    parser.add_argument("--tls-key")
    parser.add_argument("--log-level", default="INFO")
    for name, _, bind, port, username, digest in SERVICES:
        parser.add_argument("--%s-bind" % name, default=bind)
        parser.add_argument("--%s-port" % name, type=int, default=port)
        parser.add_argument("--%s-username" % name, default=username)
        parser.add_argument("--%s-password-md5" % name, default=digest)
    return parser.parse_args()


def listener(args, name, avatar_type):
    avatar = avatar_type(GoFacadeClient(args.go_url, os.environ[args.token_env]))
    auth_portal = portal.Portal(JasminPBRealm(avatar))
    checker = InMemoryUsernamePasswordDatabaseDontUse()
    checker.addUser(
        getattr(args, "%s_username" % name),
        bytes.fromhex(getattr(args, "%s_password_md5" % name)),
    )
    auth_portal.registerChecker(checker)
    factory = pb.PBServerFactory(JasminPBPortalRoot(auth_portal))
    bind = getattr(args, "%s_bind" % name)
    port = getattr(args, "%s_port" % name)
    if args.tls_cert:
        context = ssl.DefaultOpenSSLContextFactory(args.tls_key, args.tls_cert)
        return reactor.listenSSL(port, factory, context, interface=bind)
    return reactor.listenTCP(port, factory, interface=bind)


def main():
    args = options()
    if (args.tls_cert is None) != (args.tls_key is None):
        raise SystemExit("--tls-cert and --tls-key must be set together")
    if args.token_env not in os.environ or not os.environ[args.token_env]:
        raise SystemExit("%s must contain the Go facade bearer token" % args.token_env)
    logging.basicConfig(level=getattr(logging, args.log_level.upper()))
    active = []
    for name, avatar_type, _, _, _, _ in SERVICES:
        active.append(listener(args, name, avatar_type))
        logging.info("trusted %s PB listener active on %s:%d", name,
                     getattr(args, "%s_bind" % name), getattr(args, "%s_port" % name))
    reactor.run()
    return active


if __name__ == "__main__":
    main()
