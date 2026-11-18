import json, subprocess, hashlib, os, sys
target = sys.argv[1]
env = dict(os.environ, GOOS="linux", GOARCH="amd64", CGO_ENABLED="0", GOFLAGS="-mod=readonly", GOWORK="off")
pkgs = ["./cmd/server", "./internal/handler", "./internal/repo", "database/sql", "log", "net/http", "github.com/go-chi/chi/v5", "context"]
out = subprocess.run(["go", "list", "-json"] + pkgs, cwd=target, env=env, capture_output=True, text=True, check=True).stdout
dec = json.JSONDecoder(); i = 0; res = {}
goroot = subprocess.run(["go","env","GOROOT"],capture_output=True,text=True).stdout.strip()
modcache = subprocess.run(["go","env","GOMODCACHE"],capture_output=True,text=True).stdout.strip()
tabs = os.path.abspath(target)
while i < len(out):
    while i < len(out) and out[i].isspace(): i += 1
    if i >= len(out): break
    p, i = dec.raw_decode(out, i)
    files = []
    for f in sorted(p.get("GoFiles", [])):
        full = os.path.join(p["Dir"], f)
        h = hashlib.sha256(open(full, "rb").read()).hexdigest()
        if full.startswith(tabs + "/"): rel = full[len(tabs)+1:]
        elif full.startswith(modcache + "/"):
            rel = full[len(modcache)+1:].split("/", 1)[1]
        else: rel = full[len(goroot)+5:]  # strip $GOROOT/src/
        files.append((rel, h))
    files.sort()
    ch = hashlib.sha256("".join(f"{r}\x00{h}\n" for r, h in files).encode()).hexdigest()
    res[p["ImportPath"]] = {"files": [{"path": r, "sha256": "sha256:"+h} for r, h in files], "content_hash": "sha256:"+ch,
        "ignored": sorted(p.get("IgnoredGoFiles", [])), "imports": p.get("Imports", [])}
print(json.dumps(res, indent=1))
