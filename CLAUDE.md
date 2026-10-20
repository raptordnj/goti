
## Reference coding (XERJ)

Before implementing a non-trivial mechanism (segmented/multi-connection download, resume, retry/backoff, redirects, checksum verification), search how peer projects solved it and cite `project/file:line`. Check the licence before copying (aria2 is GPL-2.0: read for ideas, don't copy).

- Node: `curl -s localhost:9200/_cluster/health` (start: `nohup xerj --insecure --data-dir ~/xerj-data > ~/xerj-node.log 2>&1 &`)
- Local refs (`~/xerj-ref`: melbahja/got, cavaliergopher/grab, aria2): `curl -s -XPOST 'localhost:9200/ref-*/_search' -H 'content-type: application/json' -d '{"query":{"match":{"defs":"<words>"}},"_source":["ax_path","symbols"]}'`
- Hub corpora: `xerj code got "<question>"`, `xerj code curl-src "<question>"`
- This repo: index prefix `goti` (`goti-*`). Re-index after changes: `xerj autoindex . --prefix goti --state-dir ~/xerj-state/goti`
- MCP: the `xerj` server is registered (user scope).
