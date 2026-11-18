import json, copy, hashlib, sys
E = sys.argv[1] + '/'
def load(n): return json.load(open(E + n))
def canon(o): return json.dumps(o, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()
def diag_id(d):
    loc = d['loc'] or {}
    t = [d['kind'], d['scope'], d['unit_id'], d['symbol_id'], d['site_id'], loc.get('root'), loc.get('module'), loc.get('file'), loc.get('line'), loc.get('column'), d['message']]
    return 'diag:' + hashlib.sha256(canon(t)).hexdigest()[:16]
REL = {'global': 0, 'indeterminate': 1, 'warning': 2}
def resort(r): r['diagnostics'].sort(key=lambda d: (REL[d['relevance']], d['id']))
def recount(r):
    for a in r['assumptions']: a['exempted_count'] = sum(1 for d in r['diagnostics'] if d['exempted_by'] == a['id'])
miss, norules, bad, ill = load('report.policy-missing.json'), load('report.policy-no-rules.json'), load('report.policy-invalid.json'), load('report.illustrative.json')
cases, expect = {}, {}
def add(name, doc, want): cases[name] = doc; expect[name + '.json'] = want
# B1
r=copy.deepcopy(miss); r['status']='PASS'; r['exit_code']=0; add('b1-policy-missing-exit0', r, "at '/summary/global_diagnostics': value must be 0")
r=copy.deepcopy(miss); r['exit_code']=1; add('b1-policy-missing-exit1', r, "at '/exit_code': value must be 2")
r=copy.deepcopy(miss); r['rules']=[{'id':'db-in-repo','kind':'restrict_concept_to_boundary','status':'INDETERMINATE','violation_ids':[],'diagnostic_ids':[]}]; r['summary']['rules']=1
r['assumptions']=[{'id':'no_possible_target','statement':'x','exempted_count':0},{'id':'trusted_stdlib','statement':'x','exempted_count':0}]
add('b1-policy-missing-rules-evaluated', r, 'policy failure diagnostic present but the report has 1 rules')
r=copy.deepcopy(norules); r['diagnostics'][0]['affects_rules']=[{'rule_id':'db-in-repo','reason':'policy_config'}]; add('b1-no-rules-global-with-affects', r, "at '/diagnostics/0/affects_rules': maxItems: got 1, want 0")
r=copy.deepcopy(norules); r['diagnostics'][0]['relevance']='warning'; r['summary']['global_diagnostics']=0; r['summary']['warning_diagnostics']=1; r['status']='PASS'; r['exit_code']=0
add('b1-no-rules-as-warning', r, 'kind policy_no_rules is run-level and must have relevance global')
r=copy.deepcopy(bad); d=r['diagnostics'][0]; d['scope']='unit'; d['unit_id']='example.com/demo'; d['id']=diag_id(d); add('b1-invalid-global-relevance-unit-scope', r, "at '/diagnostics/0/scope': value must be 'global'")
r=copy.deepcopy(bad); r['summary']['global_diagnostics']=0; add('b1-invalid-summary-mismatch', r, "at '/summary': 'anyOf' failed")
# S1
r=copy.deepcopy(ill); r['assumptions']=[a for a in r['assumptions'] if a['id']!='no_possible_target']; add('s1-missing-no-possible-target', r, 'assumption no_possible_target is not listed')
r=copy.deepcopy(ill); r['assumptions'].reverse(); add('s1-assumptions-unsorted', r, 'assumptions not strictly sorted')
r=copy.deepcopy(ill); r['assumptions'].append(copy.deepcopy(r['assumptions'][1])); add('s1-assumptions-duplicate', r, 'are equal')
r=copy.deepcopy(ill); [a for a in r['assumptions'] if a['id']=='trusted_stdlib'][0]['exempted_count']=4410; add('s1-count-does-not-reconcile', r, 'assumption trusted_stdlib exempted_count 4410, but 2 diagnostics')
r=copy.deepcopy(ill); [d for d in r['diagnostics'] if d['exempted_by']=='no_possible_target'][0]['exempted_by']='trusted_stdlib'; recount(r)
add('s1-trusted-stdlib-on-chi', r, 'trusted_stdlib exemption needs an unknown-call kind with unit_origin stdlib')
r=copy.deepcopy(ill); d=[d for d in r['diagnostics'] if d['exempted_by']=='trusted_stdlib'][0]; d['relevance']='indeterminate'; d['affects_rules']=[{'rule_id':'db-in-repo','reason':'reachable_from_governed_entry'}]
add('s1-exempted-but-indeterminate', r, "exempted_by': got string, want null")
# S1 provenance: a dotless module path is not the standard library.
def dotless(origin):
    r=copy.deepcopy(ill)
    d=[d for d in r['diagnostics'] if d['exempted_by']=='trusted_stdlib'][0]
    d['unit_id']='app/internal/helper'; d['symbol_id']='app/internal/helper.Run'; d['site_id']='app/internal/helper.Run#0'
    d['loc']={'root':'repo','module':None,'file':'internal/helper/helper.go','line':12,'column':9}
    d['unit_origin']=origin; d['id']=diag_id(d); resort(r); return r
add('s1-dotless-module-trusted', dotless('module'), 'trusted_stdlib exemption needs an unknown-call kind with unit_origin stdlib and a unit in the go1.26.11 standard-library inventory (got unresolved_interface_call, unit_origin "module", unit "app/internal/helper")')
add('s1-dotless-module-claims-stdlib', dotless('stdlib'), 'unit_origin stdlib for "app/internal/helper" contradicts the go1.26.11 standard-library inventory')
r=copy.deepcopy(ill); d=[d for d in r['diagnostics'] if d['exempted_by']=='trusted_stdlib'][0]; del d['unit_origin']
add('s1-trusted-without-provenance', r, 'unit_origin ""')
sh=load('shard.spike-cmd-server.json'); sh['unit']['origin']='stdlib'; sh['unit']['module']=None
add('s1-shard-module-unit-claims-stdlib', sh, 'unit example.com/spike/cmd/server: origin stdlib contradicts the go1.26.11 standard-library inventory')
# 0.4: rule_not_evaluated, source status
dc=load('report.demo-clean-step2.json')
def ne(r): return [d for d in r['diagnostics'] if d['kind']=='rule_not_evaluated'][0]
r=copy.deepcopy(dc); d=ne(r); d['relevance']='warning'; d['affects_rules']=[]
add('v04-not-evaluated-as-warning', r, 'rule_not_evaluated must be global, indeterminate, and affect exactly one rule with reason not_evaluated')
r=copy.deepcopy(dc); ne(r)['affects_rules'][0]['reason']='unit_in_scope'
add('v04-not-evaluated-wrong-reason', r, 'rule_not_evaluated must be global, indeterminate')
r=copy.deepcopy(dc); d=ne(r); r['rules']=[x for x in r['rules'] if x['id']!=d['affects_rules'][0]['rule_id']]; r['summary']['rules']=len(r['rules'])
add('v04-not-evaluated-rule-missing', r, 'diagnostic affects unknown rule')
r=copy.deepcopy(dc); [x for x in r['rules'] if x['status']=='INDETERMINATE'][0]['status']='PASS'
add('v04-unevaluated-rule-reported-pass', r, "diagnostic_ids': maxItems: got 1, want 0")
mi=load('shard.regressmissing-absent-missing.json')
x=copy.deepcopy(mi); x['diagnostics']=[]
add('v04-missing-without-error', x, 'source_status missing needs an error diagnostic')
x=copy.deepcopy(mi); x['unit']['source_status']='compiled'
add('v04-compiled-without-files', x, "minItems: got 0, want 1")
x=copy.deepcopy(mi); x['unit']['origin']='unresolved'; x['unit']['source_status']='builtin'
add('v04-unresolved-builtin', x, "value must be 'stdlib'")
sp=load('shard.spike-cmd-server.json'); sp['unit']['source_status']='builtin'; sp['unit']['files']=[]
add('v04-builtin-not-unsafe', sp, "value must be 'stdlib'")
for k,v in cases.items():
    json.dump(v,open(E+'invalid/'+k+'.json','w'),indent=2); open(E+'invalid/'+k+'.json','a').write('\n')
json.dump(expect,open(E+'invalid/expect.json','w'),indent=2,sort_keys=True); open(E+'invalid/expect.json','a').write('\n')
print(len(cases),'negative fixtures')
