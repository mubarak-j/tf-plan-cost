#!/usr/bin/env python3
"""Writes demo.tfstate: a fake state matching main.tf, so pull requests to the
demo show real changes (not the whole stack as new) without an AWS account.

  terraform init && terraform plan -out=tfplan   # no state: everything is new
  terraform show -json tfplan > /tmp/plan.json
  terraform providers schema -json > /tmp/schema.json
  python3 tools/make-state.py /tmp/plan.json /tmp/schema.json demo.tfstate
  rm tfplan

Values Terraform only knows after apply get typed placeholders, so a plan
with -refresh=false against this state shows "No changes".
"""
plan=json.load(open(sys.argv[1])); schema=json.load(open(sys.argv[2])); out=sys.argv[3]
rs=schema["provider_schemas"]["registry.terraform.io/hashicorp/aws"]["resource_schemas"]
def zero(t, name, addr):
    if t=="string":
        if name=="id": return "demo-"+hashlib.sha1(addr.encode()).hexdigest()[:12]
        if name.endswith("arn"): return "arn:aws:demo:us-east-1:000000000000:"+addr
        return ""
    if t=="number": return 0
    if t=="bool": return False
    if isinstance(t,list):
        if t[0] in ("list","set"): return []
        if t[0]=="map": return {}
        if t[0]=="object": return {k:zero(v,k,addr) for k,v in t[1].items()}
    return None
def fill(v, unknown, block, addr):
    # block: schema block for v (dict with attributes / block_types)
    if not isinstance(v, dict): v = {}
    out=dict(v)
    attrs=block.get("attributes",{}); btypes=block.get("block_types",{})
    for k,spec in attrs.items():
        u = unknown.get(k) if isinstance(unknown,dict) else None
        if u is True:
            out[k]=zero(spec["type"],k,addr)
        elif k not in out:
            out[k]=None
    for k,bt in btypes.items():
        u = unknown.get(k) if isinstance(unknown,dict) else None
        cur = out.get(k)
        if u is True:
            out[k]=[]
        elif isinstance(cur,list):
            out[k]=[fill(x, (u[i] if isinstance(u,list) and i<len(u) else {}), bt["block"], addr) for i,x in enumerate(cur)]
        elif k not in out:
            out[k]=[]
    return out
groups={}
for rc in plan["resource_changes"]:
    if rc["mode"]!="managed": continue
    ch=rc["change"]; t=rc["type"]
    attrs=fill(ch["after"], ch.get("after_unknown",{}), rs[t]["block"], rc["address"])
    g=groups.setdefault((t,rc["name"]),{"mode":"managed","type":t,"name":rc["name"],
        "provider":'provider["registry.terraform.io/hashicorp/aws"]',"instances":[]})
    sens=[[{"type":"get_attr","value":k}] for k,v in (ch.get("after_sensitive") or {}).items() if v is True]
    inst={"schema_version":rs[t].get("version",0),"attributes":attrs,"sensitive_attributes":sens}
    if "index" in rc: inst["index_key"]=rc["index"]
    g["instances"].append(inst)
state={"version":4,"terraform_version":plan["terraform_version"],"serial":1,
       "lineage":"00000000-0000-4000-8000-000000000000","outputs":{},"resources":list(groups.values()),"check_results":None}
json.dump(state,open(out,"w"),indent=2); print("resources:",len(groups))
