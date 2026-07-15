#!/usr/bin/env python3
"""Capture frozen RandomRoundrobin and Failover route contracts."""
import argparse,json
from pathlib import Path
from unittest.mock import patch
from jasmin.routing.Filters import TransparentFilter,UserFilter,ConnectorFilter
from jasmin.routing.Routables import RoutableSubmitSm,RoutableDeliverSm
from jasmin.routing.Routes import RandomRoundrobinMTRoute,RandomRoundrobinMORoute,FailoverMTRoute,FailoverMORoute
from jasmin.routing.jasminApi import Group,User,HttpConnector,SmppClientConnector,SmppServerSystemIdConnector
from smpp.pdu.operations import SubmitSM,DeliverSM
BASELINE="0aac58e466d583d0f0436df7b8afa3dc96191263";ROOT=Path(__file__).resolve().parents[2];OUT=ROOT/"compat/fixtures/multi-connector-routes/baseline.json"
def c(cid,kind):
 if kind=="smppc":return SmppClientConnector(cid)
 if kind=="smpps":return SmppServerSystemIdConnector(cid)
 return HttpConnector(cid,"http://127.0.0.1")
def mt(uid=1):return RoutableSubmitSm(SubmitSM(source_addr=b"x",destination_addr=b"1",short_message=b"x"),User(uid,Group(100),"u","p"))
def mo(cid="src"):return RoutableDeliverSm(DeliverSM(source_addr=b"x",destination_addr=b"1",short_message=b"x"),HttpConnector(cid,"http://127.0.0.1"))
def run(case_id,build,action,input_doc):
 try:r=build();expected=action(r);error=None
 except Exception as e:expected=None;error=type(e).__name__
 return {"id":case_id,"source":"jasmin/routing/Routes.py","input":input_doc,"expected":expected,"error_type":error}
def main():
 a=c("aaa","smppc");b=c("bbb","smppc");h1=c("http1","http");h2=c("http2","http");s1=c("smpps1","smpps")
 u=User(1,Group(100),"u","p");tf=[TransparentFilter()];uf=[UserFilter(u)];cf=[ConnectorFilter(h1)]
 cases=[]
 for direction,cls,conns,rate in [("mt",RandomRoundrobinMTRoute,[a,b],2.3),("mo",RandomRoundrobinMORoute,[h1,h2],None)]:
  for idx in (0,1):
   build=(lambda cls=cls,conns=conns,rate=rate:cls(tf,conns,rate) if rate is not None else cls(tf,conns))
   def act(r,idx=idx):
    with patch("jasmin.routing.Routes.random.choice",side_effect=lambda seq:seq[idx]):x=r.getConnector()
    return {"connector_id":x.cid,"rate":r.getRate()}
   cases.append(run(f"random_{direction}_index_{idx}",build,act,{"policy":"random","direction":direction,"connectors":[{"id":x.cid,"type":x._type} for x in conns],"index":idx,"rate":rate or 0,"filter":"transparent"}))
 cases.append(run("random_mo_hybrid_allowed",lambda:RandomRoundrobinMORoute(tf,[h1,s1]),lambda r:{"connector_types":[x._type for x in r.connector]},{"policy":"random","direction":"mo","connectors":[{"id":"http1","type":"http"},{"id":"smpps1","type":"smpps"}],"filter":"transparent"}))
 cases.append(run("random_mt_filter_match",lambda:RandomRoundrobinMTRoute(uf,[a,b],0.0),lambda r:r.matchFilters(mt(1)),{"policy":"random","direction":"mt","connectors":[{"id":"aaa","type":"smppc"},{"id":"bbb","type":"smppc"}],"rate":0,"filter":"user","match":True}))
 cases.append(run("random_mt_filter_miss",lambda:RandomRoundrobinMTRoute(uf,[a,b],0.0),lambda r:r.matchFilters(mt(2)),{"policy":"random","direction":"mt","connectors":[{"id":"aaa","type":"smppc"},{"id":"bbb","type":"smppc"}],"rate":0,"filter":"user","match":False}))
 cases.append(run("random_empty_rejected",lambda:RandomRoundrobinMTRoute(tf,[],0.0),lambda r:None,{"policy":"random","direction":"mt","connectors":[],"rate":0,"filter":"transparent"}))
 cases.append(run("failover_mt_sequence",lambda:FailoverMTRoute(tf,[a,b],1.5),lambda r:{"sequence":[(x.cid if x else None) for x in [r.getConnector(),r.getConnector(),r.getConnector()]],"rate":r.getRate()},{"policy":"failover","direction":"mt","connectors":[{"id":"aaa","type":"smppc"},{"id":"bbb","type":"smppc"}],"rate":1.5,"filter":"transparent","action":"sequence"}))
 cases.append(run("failover_mo_sequence",lambda:FailoverMORoute(tf,[h1,h2]),lambda r:{"sequence":[(x.cid if x else None) for x in [r.getConnector(),r.getConnector(),r.getConnector()]],"rate":r.getRate()},{"policy":"failover","direction":"mo","connectors":[{"id":"http1","type":"http"},{"id":"http2","type":"http"}],"rate":0,"filter":"transparent","action":"sequence"}))
 def reset(r):
  first=r.getConnector().cid;r.matchFilters(mo());return {"before_reset":first,"after_reset":r.getConnector().cid}
 cases.append(run("failover_match_resets_sequence",lambda:FailoverMORoute(tf,[h1,h2]),reset,{"policy":"failover","direction":"mo","connectors":[{"id":"http1","type":"http"},{"id":"http2","type":"http"}],"rate":0,"filter":"transparent","action":"reset"}))
 cases.append(run("failover_get_connectors_order",lambda:FailoverMORoute(tf,[h1,h2]),lambda r:[x.cid for x in r.getConnectors()],{"policy":"failover","direction":"mo","connectors":[{"id":"http1","type":"http"},{"id":"http2","type":"http"}],"rate":0,"filter":"transparent","action":"pool"}))
 cases.append(run("failover_mo_mixed_rejected",lambda:FailoverMORoute(tf,[h1,s1]),lambda r:None,{"policy":"failover","direction":"mo","connectors":[{"id":"http1","type":"http"},{"id":"smpps1","type":"smpps"}],"rate":0,"filter":"transparent"}))
 cases.append(run("failover_empty_rejected",lambda:FailoverMTRoute(tf,[],0.0),lambda r:None,{"policy":"failover","direction":"mt","connectors":[],"rate":0,"filter":"transparent"}))
 cases.append(run("failover_mo_filter_match",lambda:FailoverMORoute(cf,[h1,h2]),lambda r:r.matchFilters(mo("http1")),{"policy":"failover","direction":"mo","connectors":[{"id":"http1","type":"http"},{"id":"http2","type":"http"}],"rate":0,"filter":"connector","match":True}))
 cases.append(run("failover_mo_filter_miss",lambda:FailoverMORoute(cf,[h1,h2]),lambda r:r.matchFilters(mo("other")),{"policy":"failover","direction":"mo","connectors":[{"id":"http1","type":"http"},{"id":"http2","type":"http"}],"rate":0,"filter":"connector","match":False}))
 doc={"schema_version":1,"baseline_commit":BASELINE,"source":"jasmin/routing/Routes.py","cases":cases};args=argparse.ArgumentParser();args.add_argument("--output",type=Path,default=OUT);o=args.parse_args().output;o.parent.mkdir(parents=True,exist_ok=True);o.write_text(json.dumps(doc,indent=2,sort_keys=True)+"\n");print(f"multi_connector_golden={o} cases={len(cases)} status=ok")
if __name__=="__main__":main()
