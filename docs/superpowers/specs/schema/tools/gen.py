import json, hashlib, sys, os

H = json.load(open(os.path.join(os.path.dirname(os.path.abspath(__file__)), 'hashes.json')))
OUT = sys.argv[1]

def canon(o):
    return json.dumps(o, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()

def sha(b):
    return 'sha256:' + hashlib.sha256(b).hexdigest()

def content_hash(files):
    return sha(''.join(f"{f['path']}\x00{f['sha256'][7:]}\n" for f in files).encode())

def diag_id(d):
    loc = d['loc'] or {}
    t = [d['kind'], d['scope'], d['unit_id'], d['symbol_id'], d['site_id'],
         loc.get('root'), loc.get('module'), loc.get('file'), loc.get('line'), loc.get('column'), d['message']]
    return 'diag:' + hashlib.sha256(canon(t)).hexdigest()[:16]

def viol_id(v):
    loc = v['location'] or {}
    t = [v['rule_id'], loc.get('file'), loc.get('line'), loc.get('column'),
         [[e['kind'], e['from'], e['to']] for e in v['evidence_chain']]]
    return 'viol:' + hashlib.sha256(canon(t)).hexdigest()[:16]

def L(file, line, col, root='repo', module=None):
    return {'root': root, 'module': module, 'file': file, 'line': line, 'column': col}

PROFILE = {'goos': 'linux', 'goarch': 'amd64', 'cgo_enabled': False, 'tags': [],
           'go_version': 'go1.26.11', 'x_tools_version': 'v0.52.0'}
PDIG = sha(canon(PROFILE))

def sym(id, unit, kind, name, exported=False, has_body=True, in_universe=True, parent=None,
        origin=None, wk=None, main=False, init=False, addr=False, loc=None):
    return {'id': id, 'unit_id': unit, 'kind': kind, 'name': name, 'exported': exported,
            'has_body': has_body, 'in_universe': in_universe, 'parent_id': parent,
            'origin_id': origin, 'wrapper_kind': wk,
            'entry_hints': {'is_main': main, 'is_init': init, 'address_taken': addr}, 'loc': loc}

def site(caller, k, mode, hint, targets, loc):
    return {'id': f'{caller}#{k}', 'caller': caller, 'ordinal': k, 'mode': mode, 'callee_hint': hint,
            'targets': sorted(set(targets)), 'resolution': 'static' if mode == 'static' else 'vta', 'loc': loc}

def finish_shard(unit, symbols, sites, imports, routes, mws, diags):
    for d in diags:
        d['id'] = diag_id(d)
    own = {s['id'] for s in symbols}
    ext = {}
    def ref(sid, u):
        if sid not in own:
            ext[sid] = u
    return own, ext, ref

# ---------------------------------------------------------------- real shard
U = 'example.com/spike/cmd/server'
CHI = 'github.com/go-chi/chi/v5@v5.5.0'
unitinfo = H[U]
imports_src = [('database/sql', 5), ('log', 6), ('net/http', 7), ('github.com/go-chi/chi/v5', 9),
               ('example.com/spike/internal/handler', 11), ('example.com/spike/internal/repo', 12)]
MAIN = 'example.com/spike/cmd/server.main'
INIT = 'example.com/spike/cmd/server.init'
F = 'cmd/server/main.go'
symbols = [
    sym(INIT, U, 'func', 'init', init=True, loc=None),
    sym(MAIN, U, 'func', 'main', main=True, loc=L(F, 15, 6)),
]
owner = {
    'database/sql.Open': 'database/sql', 'log.Fatal': 'log', 'example.com/spike/internal/repo.New': 'example.com/spike/internal/repo',
    'github.com/go-chi/chi/v5.NewRouter': 'github.com/go-chi/chi/v5', '(*github.com/go-chi/chi/v5.Mux).Post': 'github.com/go-chi/chi/v5',
    'net/http.ListenAndServe': 'net/http', 'database/sql.init': 'database/sql', 'log.init': 'log', 'net/http.init': 'net/http',
    'github.com/go-chi/chi/v5.init': 'github.com/go-chi/chi/v5', 'example.com/spike/internal/handler.init': 'example.com/spike/internal/handler',
    'example.com/spike/internal/repo.init': 'example.com/spike/internal/repo',
    '(*example.com/spike/internal/handler.Items).Create': 'example.com/spike/internal/handler',
    '(*example.com/spike/internal/handler.Items).Create$bound': 'example.com/spike/internal/handler',
}
sites = []
for k, t in enumerate(['database/sql.init', 'log.init', 'net/http.init', 'github.com/go-chi/chi/v5.init',
                       'example.com/spike/internal/handler.init', 'example.com/spike/internal/repo.init']):
    sites.append(site(INIT, k, 'static', t, [t], None))
for k, (t, ln, col) in enumerate([('database/sql.Open', 17, 21), ('log.Fatal', 19, 12), ('example.com/spike/internal/repo.New', 21, 37),
                                  ('github.com/go-chi/chi/v5.NewRouter', 23, 20), ('(*github.com/go-chi/chi/v5.Mux).Post', 24, 8),
                                  ('net/http.ListenAndServe', 26, 31), ('log.Fatal', 26, 11)]):
    sites.append(site(MAIN, k, 'static', t, [t], L(F, ln, col)))
routes = [{
    'id': f'route:{U}:POST:/items#0', 'unit_id': U, 'router': 'chi', 'method': 'POST', 'path': '/items',
    'pattern_raw': '/items', 'duplicate_index': 0,
    'handler_sym': '(*example.com/spike/internal/handler.Items).Create',
    'handler_value_sym': '(*example.com/spike/internal/handler.Items).Create$bound',
    'registered_in': MAIN, 'loc': L(F, 24, 8)}]
ext = {}
for s in sites:
    for t in s['targets']:
        ext[t] = owner[t]
for r in routes:
    for k in ('handler_sym', 'handler_value_sym'):
        ext[r[k]] = owner[r[k]]
shard = {
    'schema_version': '0.4', 'kind': 'unit_shard', 'build_profile_digest': PDIG,
    'unit': {'id': U, 'lang': 'go', 'origin': 'module', 'module': 'example.com/spike', 'dir': 'cmd/server',
             'source_status': 'compiled', 'files': unitinfo['files'], 'excluded_files': [], 'content_hash': unitinfo['content_hash'],
             'dep_hashes': sorted([{'unit_id': u, 'content_hash': H[u]['content_hash']} for u, _ in imports_src], key=lambda x: x['unit_id'])},
    'symbols': sorted(symbols, key=lambda s: s['id']),
    'external_symbols': [{'id': i, 'unit_id': u} for i, u in sorted(ext.items())],
    'imports': sorted([{'from_unit': U, 'to_unit': u, 'to_path': u, 'loc': L(F, ln, 2)} for u, ln in imports_src],
                      key=lambda x: (x['to_unit'], x['loc']['file'], x['loc']['line'], x['loc']['column'])),
    'call_sites': sorted(sites, key=lambda s: (s['caller'], s['ordinal'])),
    'routes': routes, 'route_middleware': [], 'diagnostics': [],
}
assert content_hash(shard['unit']['files']) == unitinfo['content_hash']
json.dump(shard, open(os.path.join(OUT, 'shard.spike-cmd-server.json'), 'w'), indent=2)
open(os.path.join(OUT, 'shard.spike-cmd-server.json'), 'a').write('\n')

# --------------------------------------------------------- illustrative shard
A = 'example.com/app/internal/api'
AF = 'internal/api/api.go'
files = [{'path': 'internal/api/api.go', 'sha256': sha(b'illustrative api.go')},
         {'path': 'internal/api/map.go', 'sha256': sha(b'illustrative map.go')}]
dep = lambda u: {'unit_id': u, 'content_hash': sha(('illustrative ' + u).encode())}
REG = f'{A}.Register'
CLO = f'{REG}$1'
MAP = f'{A}.Map'
MAPI = f'{A}.Map[int, string]'
LIST = f'(*{A}.Handler).List'
LISTB = f'(*{A}.Handler).List$bound'
PING = f'({A}.Handler).Ping'
PINGP = f'(*{A}.Handler).Ping'
RUN = f'(*{A}.Handler).Run'
AUTH = 'example.com/app/internal/auth.RequireAuth'
syms = [
    sym(REG, A, 'func', 'Register', exported=True, addr=False, loc=L(AF, 12, 6)),
    sym(CLO, A, 'closure', 'Register$1', parent=REG, addr=True, loc=L(AF, 16, 22)),
    sym(MAP, A, 'func', 'Map', exported=True, loc=L('internal/api/map.go', 4, 6)),
    sym(MAPI, A, 'instance', 'Map[int, string]', exported=True, origin=MAP, loc=L('internal/api/map.go', 4, 6)),
    sym(LIST, A, 'method', 'List', exported=True, loc=L(AF, 24, 19)),
    sym(LISTB, A, 'wrapper', 'List$bound', exported=True, origin=LIST, wk='bound', addr=True, loc=None),
    sym(PING, A, 'method', 'Ping', exported=True, loc=L(AF, 30, 18)),
    sym(PINGP, A, 'wrapper', 'Ping', exported=True, origin=PING, wk='pointer', loc=None),
    sym(RUN, A, 'method', 'Run', exported=True, loc=L(AF, 34, 19)),
    sym(f'{A}.Handler', A, 'type', 'Handler', exported=True, has_body=False, in_universe=False, loc=L(AF, 8, 6)),
]
CHIMOD = 'github.com/go-chi/chi/v5'
GET = f'({CHIMOD}.Router).Get'
WITH = f'({CHIMOD}.Router).With'
POST = f'({CHIMOD}.Router).Post'
MUXGET = f'(*{CHIMOD}.Mux).Get'
MUXWITH = f'(*{CHIMOD}.Mux).With'
MUXPOST = f'(*{CHIMOD}.Mux).Post'
IMUXPOST = f'(*{CHIMOD}.Mux).Post'
s2 = [
    site(REG, 0, 'invoke', GET, [MUXGET], L(AF, 13, 7)),
    site(REG, 1, 'invoke', GET, [MUXGET], L(AF, 14, 7)),
    site(REG, 2, 'invoke', WITH, [MUXWITH], L(AF, 15, 8)),
    site(REG, 3, 'invoke', POST, [MUXPOST], L(AF, 15, 30)),
    site(REG, 4, 'invoke', GET, [MUXGET], L(AF, 16, 7)),
    site(REG, 5, 'static', MAPI, [MAPI], L(AF, 20, 5)),
    site(CLO, 0, 'invoke', '(net/http.ResponseWriter).WriteHeader',
         ['(*net/http.response).WriteHeader', '(*net/http.http2responseWriter).WriteHeader'], L(AF, 17, 14)),
    site(LISTB, 0, 'static', LIST, [LIST], None),
    site(PINGP, 0, 'static', PING, [PING], None),
    site(RUN, 0, 'static', '(reflect.Value).Call', ['(reflect.Value).Call'], L(AF, 35, 14)),
]
CREATE = f'(*{A}.Handler).Create'
CREATEB = f'(*{A}.Handler).Create$bound'
syms.append(sym(CREATE, A, 'method', 'Create', exported=True, loc=L(AF, 27, 19)))
syms.append(sym(CREATEB, A, 'wrapper', 'Create$bound', exported=True, origin=CREATE, wk='bound', addr=True, loc=None))
s2.append(site(CREATEB, 0, 'static', CREATE, [CREATE], None))
def rid(m, p, k): return f'route:{A}:{m}:{p}#{k}'
routes2 = [
    {'id': rid('GET', '/items', 0), 'unit_id': A, 'router': 'chi', 'method': 'GET', 'path': '/items', 'pattern_raw': '/items',
     'duplicate_index': 0, 'handler_sym': LIST, 'handler_value_sym': LISTB, 'registered_in': REG, 'loc': L(AF, 13, 7)},
    {'id': rid('GET', '/items', 1), 'unit_id': A, 'router': 'chi', 'method': 'GET', 'path': '/items', 'pattern_raw': '/items',
     'duplicate_index': 1, 'handler_sym': LIST, 'handler_value_sym': LISTB, 'registered_in': REG, 'loc': L(AF, 14, 7)},
    {'id': rid('POST', '/items', 0), 'unit_id': A, 'router': 'chi', 'method': 'POST', 'path': '/items', 'pattern_raw': '/items',
     'duplicate_index': 0, 'handler_sym': CREATE, 'handler_value_sym': CREATEB, 'registered_in': REG, 'loc': L(AF, 15, 30)},
    {'id': rid('GET', '/healthz', 0), 'unit_id': A, 'router': 'chi', 'method': 'GET', 'path': '/healthz', 'pattern_raw': '/healthz',
     'duplicate_index': 0, 'handler_sym': CLO, 'handler_value_sym': None, 'registered_in': REG, 'loc': L(AF, 16, 7)},
]
routes2.sort(key=lambda r: r['id'])
mws2 = [{'route_id': rid('POST', '/items', 0), 'middleware_sym': AUTH, 'order': 0, 'source': 'with', 'loc': L(AF, 15, 8)}]
diags2 = [
    {'kind': 'duplicate_route', 'severity': 'warning', 'scope': 'unit', 'unit_id': A, 'symbol_id': REG, 'site_id': f'{REG}#1',
     'message': 'GET /items is registered twice in this unit; both registrations are kept as separate routes.', 'loc': L(AF, 14, 7)},
    {'kind': 'reflection_call', 'severity': 'warning', 'scope': 'unit', 'unit_id': A, 'symbol_id': RUN, 'site_id': f'{RUN}#0',
     'message': 'reflect.Value.Call in module code: callees are not known to the call graph.', 'loc': L(AF, 35, 14)},
]
for d in diags2:
    d['id'] = diag_id(d)
own = {s['id'] for s in syms}
ext2 = {}
for s in s2:
    for t in s['targets'] + ([s['callee_hint']] if s['mode'] == 'static' else []):
        if t not in own:
            ext2[t] = CHIMOD if CHIMOD in t else ('net/http' if 'net/http' in t else ('reflect' if 'reflect.' in t else None))
ext2[AUTH] = 'example.com/app/internal/auth'
assert None not in ext2.values(), ext2
shard2 = {
    'schema_version': '0.4', 'kind': 'unit_shard', 'build_profile_digest': PDIG,
    'unit': {'id': A, 'lang': 'go', 'origin': 'module', 'module': 'example.com/app', 'dir': 'internal/api',
             'source_status': 'compiled', 'files': files, 'excluded_files': [{'path': 'internal/api/api_windows.go', 'reason': 'build_constraint'},
                                                {'path': 'internal/api/api_test.go', 'reason': 'test_file'}],
             'content_hash': content_hash(files),
             'dep_hashes': [dep('example.com/app/internal/auth'), dep(CHIMOD), dep('net/http')]},
    'symbols': sorted(syms, key=lambda s: s['id']),
    'external_symbols': [{'id': i, 'unit_id': u} for i, u in sorted(ext2.items())],
    'imports': [
        {'from_unit': A, 'to_unit': 'example.com/app/internal/auth', 'to_path': 'example.com/app/internal/auth', 'loc': L(AF, 5, 2)},
        {'from_unit': A, 'to_unit': CHIMOD, 'to_path': CHIMOD, 'loc': L(AF, 4, 2)},
        {'from_unit': A, 'to_unit': 'net/http', 'to_path': 'net/http', 'loc': L(AF, 3, 2)},
    ],
    'call_sites': sorted(s2, key=lambda s: (s['caller'], s['ordinal'])),
    'routes': routes2, 'route_middleware': mws2, 'diagnostics': sorted(diags2, key=lambda d: d['id']),
}
json.dump(shard2, open(os.path.join(OUT, 'shard.illustrative-api.json'), 'w'), indent=2)
open(os.path.join(OUT, 'shard.illustrative-api.json'), 'a').write('\n')

# ------------------------------------------------------------ manifest example
gd = [{'kind': 'unsupported_layout', 'severity': 'warning', 'scope': 'global', 'unit_id': None, 'symbol_id': None,
       'site_id': None, 'message': 'go.work found at repository root and ignored (GOWORK=off); only module example.com/app is analyzed.',
       'loc': L('go.work', 1, 1)}]
for d in gd:
    d['id'] = diag_id(d)
entries = sorted(['example.com/app/cmd/api.init', 'example.com/app/cmd/api.main', LIST, CREATE, CLO, RUN])
manifest = {
    'schema_version': '0.4', 'kind': 'analysis_manifest',
    'analyzer': {'name': 'bedrock-analyzer-go', 'version': '0.0.0-poc'}, 'status': 'complete',
    'build_profile': PROFILE, 'build_profile_digest': PDIG,
    'module': {'path': 'example.com/app', 'dir': '.'}, 'patterns': ['./...'],
    'limits': {'timeout_seconds': 120, 'memory_mib': 4096},
    'universe': {'definition': 'all_functions_transitive_closure_v1', 'bodyless_policy': 'leaf_with_diagnostic_v1',
                 'function_count': 17012, 'bodyless_count': 549},
    'units': sorted([
        {'id': A, 'origin': 'module', 'content_hash': shard2['unit']['content_hash'], 'shard': sha(canon(shard2))},
        {'id': 'example.com/app/internal/auth', 'origin': 'module', 'content_hash': dep('example.com/app/internal/auth')['content_hash'], 'shard': sha(b'illustrative auth shard')},
        {'id': 'example.com/app/internal/auth/private', 'origin': 'module', 'content_hash': sha(b'illustrative auth/private'), 'shard': sha(b'illustrative auth/private shard')},
        {'id': 'example.com/app/internal/billing', 'origin': 'module', 'content_hash': sha(b'illustrative billing'), 'shard': sha(b'illustrative billing shard')},
        {'id': CHIMOD, 'origin': 'dependency', 'content_hash': dep(CHIMOD)['content_hash'], 'shard': sha(b'illustrative chi shard')},
        {'id': 'net/http', 'origin': 'stdlib', 'content_hash': dep('net/http')['content_hash'], 'shard': sha(b'illustrative net/http shard')},
    ], key=lambda u: u['id']),
    'excluded_packages': [{'id': 'example.com/app/tools', 'reason': 'build_constraint'}],
    'entry': {'count': len(entries), 'digest': sha(''.join(e + '\n' for e in entries).encode())},
    'global_diagnostics': gd,
}
json.dump(manifest, open(os.path.join(OUT, 'analysis.illustrative.json'), 'w'), indent=2)
open(os.path.join(OUT, 'analysis.illustrative.json'), 'a').write('\n')

NPT = 'A non-static call with no possible target in the analyzed universe (VTA over the closed whole program, cgo disabled) is not an unknown call.'
TSL = 'Unknown calls owned by the pinned go1.26.11 standard library are not counted (owner decision A, plan section 7; policy contract section 7).'
# --------------------------------------------------------------- report example
R_IMP, R_MW, R_DB = 'auth-private-import', 'routes-require-auth', 'db-in-repo'
v1 = {'rule_id': R_IMP, 'rule_kind': 'forbid_import',
      'location': L('internal/billing/billing.go', 6, 2),
      'message': 'example.com/app/internal/billing imports example.com/app/internal/auth/private, which is forbidden outside example.com/app/internal/auth/**.',
      'evidence_chain': [{'kind': 'import', 'from': 'example.com/app/internal/billing', 'to': 'example.com/app/internal/auth/private',
                          'loc': L('internal/billing/billing.go', 6, 2)}],
      'remediation': {'message': 'Use the public auth API instead of auth/private.', 'suggest': ['example.com/app/internal/auth']}}
v2 = {'rule_id': R_MW, 'rule_kind': 'require_middleware', 'location': L(AF, 13, 7),
      'message': 'Route GET /items (registration #0) is not wrapped by auth.middleware.',
      'evidence_chain': [{'kind': 'route', 'from': rid('GET', '/items', 0), 'to': LIST, 'loc': L(AF, 13, 7)},
                         {'kind': 'middleware_missing', 'from': rid('GET', '/items', 0), 'to': AUTH, 'loc': None}],
      'remediation': {'message': 'Attach auth.RequireAuth to every non-public route.', 'suggest': [AUTH]}}
v3 = dict(v2, location=L(AF, 14, 7), message='Route GET /items (registration #1) is not wrapped by auth.middleware.',
          evidence_chain=[{'kind': 'route', 'from': rid('GET', '/items', 1), 'to': LIST, 'loc': L(AF, 14, 7)},
                          {'kind': 'middleware_missing', 'from': rid('GET', '/items', 1), 'to': AUTH, 'loc': None}])
viols = [v1, v2, v3]
for v in viols:
    v['id'] = viol_id(v)
viols.sort(key=lambda v: (v['rule_id'], v['location']['file'], v['location']['line'], v['location']['column'], v['id']))
rd = []
d_dyn = next(d for d in diags2 if d['kind'] == 'reflection_call')
d_dup = next(d for d in diags2 if d['kind'] == 'duplicate_route')
rd.append(dict(d_dyn, relevance='indeterminate', affects_rules=[{'rule_id': R_DB, 'reason': 'reachable_from_governed_entry'}], exempted_by=None))
rd.append(dict(d_dup, relevance='warning', affects_rules=[], exempted_by=None))
rd.append(dict(gd[0], relevance='warning', affects_rules=[], exempted_by=None))
# Unknown-call diagnostics exempted under policy contract section 7 (decision A).
exempt = [
    ({'kind': 'unresolved_interface_call', 'severity': 'warning', 'scope': 'unit', 'unit_id': 'net/http',
      'symbol_id': '(*net/http.conn).serve', 'site_id': '(*net/http.conn).serve#41',
      'message': 'Interface call (net/http.Handler).ServeHTTP has no VTA targets.', 'loc': L('net/http/server.go', 2106, 34, root='goroot')}, 'trusted_stdlib'),
    ({'kind': 'bodyless_function', 'severity': 'warning', 'scope': 'unit', 'unit_id': 'database/sql',
      'symbol_id': '(*database/sql.DB).conn', 'site_id': '(*database/sql.DB).conn#3',
      'message': 'Call to bodyless function runtime.nanotime.', 'loc': L('database/sql/sql.go', 1330, 21, root='goroot')}, 'trusted_stdlib'),
    ({'kind': 'unresolved_dynamic_call', 'severity': 'warning', 'scope': 'unit', 'unit_id': CHIMOD,
      'symbol_id': '(*github.com/go-chi/chi/v5.Mux).Group', 'site_id': '(*github.com/go-chi/chi/v5.Mux).Group#1',
      'message': 'Call through func value fn has no possible target in the analyzed universe.', 'loc': L('mux.go', 271, 5, root='module', module='github.com/go-chi/chi/v5@v5.5.0')}, 'no_possible_target'),
]
for d, by in exempt:
    d['id'] = diag_id(d)
    rd.append(dict(d, relevance='warning', affects_rules=[], exempted_by=by))
ORIGIN = {A: 'module', 'net/http': 'stdlib', 'database/sql': 'stdlib', CHIMOD: 'dependency'}
for d in rd:
    d['unit_origin'] = ORIGIN[d['unit_id']] if d['unit_id'] else None
REL = {'global': 0, 'indeterminate': 1, 'warning': 2}
rd.sort(key=lambda d: (REL[d['relevance']], d['id']))
rules = [
    {'id': R_IMP, 'kind': 'forbid_import', 'status': 'VIOLATION', 'violation_ids': [v1['id']], 'diagnostic_ids': []},
    {'id': R_MW, 'kind': 'require_middleware', 'status': 'VIOLATION', 'violation_ids': sorted([v2['id'], v3['id']]), 'diagnostic_ids': []},
    {'id': R_DB, 'kind': 'restrict_concept_to_boundary', 'status': 'INDETERMINATE', 'violation_ids': [], 'diagnostic_ids': [d_dyn['id']]},
]
rules.sort(key=lambda r: r['id'])
report = {
    'schema_version': '0.4', 'kind': 'report', 'tool': {'name': 'bedrock', 'version': '0.0.0-poc'},
    'status': 'VIOLATION', 'exit_code': 1, 'build_profile': PROFILE,
    'policy': {'path': '.bedrock/policy.toml', 'sha256': sha(b'illustrative policy')},
    'inputs': {'manifest_sha256': sha(canon(manifest)), 'shard_count': len(manifest['units'])},
    'assumptions': [
        {'id': 'no_possible_target', 'statement': NPT, 'exempted_count': sum(1 for d in rd if d['exempted_by'] == 'no_possible_target')},
        {'id': 'trusted_stdlib', 'statement': TSL, 'exempted_count': sum(1 for d in rd if d['exempted_by'] == 'trusted_stdlib')},
    ],
    'summary': {'violations': len(viols), 'indeterminate_diagnostics': 1, 'global_diagnostics': 0,
                'warning_diagnostics': sum(1 for d in rd if d['relevance'] == 'warning'),
                'units_analyzed': 4, 'rules': len(rules)},
    'packages': {'included': ['example.com/app/internal/api', 'example.com/app/internal/auth',
                              'example.com/app/internal/auth/private', 'example.com/app/internal/billing'],
                 'excluded': [{'id': 'example.com/app/tools', 'reason': 'build_constraint'}],
                 'excluded_files': [{'path': 'internal/api/api_test.go', 'reason': 'test_file'},
                                    {'path': 'internal/api/api_windows.go', 'reason': 'build_constraint'}]},
    'rules': rules, 'violations': viols, 'diagnostics': rd,
}
json.dump(report, open(os.path.join(OUT, 'report.illustrative.json'), 'w'), indent=2)
open(os.path.join(OUT, 'report.illustrative.json'), 'a').write('\n')
print('ok')

# ------------------------------------------------- policy-failure reports (B1)
def fail_report(name, kind, message, policy_sha, loc):
    d = {'kind': kind, 'severity': 'error', 'scope': 'global', 'unit_id': None, 'symbol_id': None,
         'site_id': None, 'message': message, 'loc': loc}
    d['id'] = diag_id(d)
    rep = {
        'schema_version': '0.4', 'kind': 'report', 'tool': {'name': 'bedrock', 'version': '0.0.0-poc'},
        'status': 'INDETERMINATE', 'exit_code': 2, 'build_profile': PROFILE,
        'policy': {'path': '.bedrock/policy.toml', 'sha256': policy_sha},
        'inputs': {'manifest_sha256': None, 'shard_count': 0},
        'assumptions': [],
        'summary': {'violations': 0, 'indeterminate_diagnostics': 0, 'global_diagnostics': 1,
                    'warning_diagnostics': 0, 'units_analyzed': 0, 'rules': 0},
        'packages': {'included': [], 'excluded': [], 'excluded_files': []},
        'rules': [], 'violations': [],
        'diagnostics': [dict(d, relevance='global', affects_rules=[], exempted_by=None, unit_origin=None)],
    }
    path = os.path.join(OUT, f'report.{name}.json')
    json.dump(rep, open(path, 'w'), indent=2)
    open(path, 'a').write('\n')

fail_report('policy-missing', 'policy_missing', '.bedrock/policy.toml not found; no rule can be evaluated.', None, None)
fail_report('policy-no-rules', 'policy_no_rules', 'Policy defines no [[rule]] entries.', sha(b'illustrative empty policy'), L('.bedrock/policy.toml', 1, 1))
fail_report('policy-invalid', 'policy_invalid', 'Unknown key "scopes" in [[rule]] #2 (did you mean "scope"?).', sha(b'illustrative bad policy'), L('.bedrock/policy.toml', 41, 1))
print('fail reports ok')
