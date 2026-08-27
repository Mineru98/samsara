# Static JSON/path/README assertions

Surface: feature worktree /Users/mineru/SourceCode/samsara-issue-1.

Exact invocation:

```sh
node --input-type=module -e 'import fs from "node:fs"; const R=process.cwd(),p=f=>R+"/"+f,req=[".grok-plugin/plugin.json",".claude-plugin/plugin.json",".codex-plugin/plugin.json",".claude-plugin/marketplace.json",".agents/plugins/marketplace.json","assets/logo.png"]; req.forEach(f=>{if(!fs.existsSync(p(f)))throw Error("missing "+f)}); const g=JSON.parse(fs.readFileSync(p(".grok-plugin/plugin.json"))); ["name","version","description","author","homepage","repository","license","keywords","logo"].forEach(k=>{if(!(k in g))throw Error("manifest missing "+k)}); [".grok-plugin/plugin.json",".claude-plugin/plugin.json",".codex-plugin/plugin.json",".claude-plugin/marketplace.json",".agents/plugins/marketplace.json"].forEach(f=>JSON.parse(fs.readFileSync(p(f)))); const skills=fs.readdirSync(p("skills")).filter(d=>fs.existsSync(p("skills/"+d+"/SKILL.md"))),agents=fs.readdirSync(p("agents")).filter(f=>f.endsWith(".md")); if(skills.length!==8||agents.length!==4)throw Error("counts "+skills.length+"/"+agents.length); const md=fs.readFileSync(p("README.md"),"utf8"),ok=[/grok plugin install/,/grok plugin update/,/함께 설치할 수 있습니다|호환|compat/i,/SHA|sha256|trust/i,/https:\/\/(?:www\.)?x\.ai\/|github\.com\/xai-org/,/Claude/,/Codex/].every(r=>r.test(md)),sh=[...md.matchAll(/@[0-9a-f]{40}/g)]; if(!ok||!sh.length)throw Error("README assertions failed"); console.log(JSON.stringify({manifest:g.name,version:g.version,jsonFiles:5,skillCount:skills.length,agentCount:agents.length,shaPins:sh.map(m=>m[0].slice(1)),readmeAssertions:ok}));'
```

Result: exit 0.

```json
{"manifest":"samsara","version":"0.1.0","jsonFiles":5,"skillCount":8,"agentCount":4,"shaPins":["fac10ac385f41c217f94d9565e0cec416288d37e"],"readmeAssertions":true}
```
