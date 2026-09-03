# Loom

Loom is a self-hostable context engine. Clients — agents, harnesses,
applications — Ingest conversational history as Sessions of Turns; Loom
derives Passages from that history and, on Retrieve, returns the Context
Bundle a client places in front of its own LLM. Answer generation always
belongs to the client, never to Loom (ADR-0001).

See `CONTEXT.md` for the full glossary (Turn, Session, Passage, Namespace,
Embedder, Reindex, Context Bundle) and `CONTEXT-MAP.md` for how the Engine
and its benchmarking harness relate.

## Contract

Loom's contract is defined in `proto/loom/v1/loom.proto` and served over
ConnectRPC (ADR-0005): one handler speaks the Connect protocol (plain
HTTP/JSON, used below), gRPC, and gRPC-Web. `LoomService` exposes four RPCs:

| RPC | Purpose |
|---|---|
| `Ingest` | Hand a Namespace one or more Sessions of Turns to the Engine. Returns a cursor. |
| `Retrieve` | Ask for the Context Bundle relevant to a query in a Namespace. |
| `Settle` | Block until everything Ingested up to a cursor is queryable via Retrieve — the settle barrier (ADR-0006). |
| `Reindex` | Rebuild derived Passages and embeddings after an Embedder swap (ADR-0008). Returns a cursor, settled the same way as Ingest. |

Ingest a Session, then Retrieve against it, over Connect JSON:

```bash
curl -s http://localhost:8080/loom.v1.LoomService/Ingest \
  -H 'Content-Type: application/json' \
  -d '{
    "namespace": "acme-support",
    "sessions": [
      {
        "id": "session-1",
        "turns": [
          { "id": "turn-1", "speaker": "user", "content": "My order #4821 never arrived.", "eventTime": "2026-01-15T09:00:00Z" },
          { "id": "turn-2", "speaker": "agent", "content": "I have flagged order #4821 for a reship.", "eventTime": "2026-01-15T09:00:12Z" }
        ]
      }
    ]
  }'

curl -s http://localhost:8080/loom.v1.LoomService/Retrieve \
  -H 'Content-Type: application/json' \
  -d '{ "namespace": "acme-support", "query": "what happened with order 4821?", "limit": 5 }'
```

## Quickstart (Docker Compose)

```bash
cp deploy/compose/.env.example deploy/compose/.env
# edit deploy/compose/.env: at minimum set CE_EMBEDDER_MODEL, CE_EMBEDDER_DIMENSIONS,
# and CE_EMBEDDER_API_KEY (or switch to the local Embedder, see below)

docker compose -f deploy/compose/docker-compose.yaml up
curl http://localhost:8080/healthz
```

This brings up `loom`, `postgres` (the v0 store — pgvector + tsvector + River,
ADR-0007), and `neo4j` (the graph layer, present per ADR-0009 but not yet
queried by v0). To run without a hosted embeddings API key, add the bundled
Embedder:

```bash
docker compose -f deploy/compose/docker-compose.yaml --profile local-embedder up
```

and set `CE_EMBEDDER_BASE_URL=http://tei:80/v1` in your `.env` (see
`deploy/compose/.env.example` for the full list of variables).

## Quickstart (Helm)

```bash
helm install loom deploy/helm/loom \
  --set config.CE_EMBEDDER_MODEL=text-embedding-3-small \
  --set config.CE_EMBEDDER_DIMENSIONS=1536 \
  --set secrets.CE_EMBEDDER_API_KEY=sk-...
```

By default the chart also installs an in-chart Postgres (`postgres.enabled`)
and Neo4j (`neo4j.enabled`) StatefulSet. Point at a Postgres you run
yourself with `--set postgres.enabled=false --set postgres.externalUrl=...`.
Enable the in-cluster Embedder with `--set embedder.local.enabled=true` (then
set `config.CE_EMBEDDER_BASE_URL` per the chart's `NOTES.txt`). Secrets can
be supplied via a Secret you manage with `--set secrets.existingSecret=...`
instead of the chart's own.

## Configuration

Every variable uses the existing `CE_` prefix (`internal/config`):

| Variable | Default | Notes |
|---|---|---|
| `CE_HTTP_ADDR` | `:8080` | Address the Engine's HTTP server listens on. Health at `/healthz`, metrics at `/metrics`. |
| `CE_ENV` | `development` | Deployment environment label. |
| `CE_DATABASE_URL` | required | Postgres DSN (ADR-0007). |
| `CE_EMBEDDER_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible embeddings endpoint. |
| `CE_EMBEDDER_API_KEY` | empty | Bearer token for `CE_EMBEDDER_BASE_URL`; header omitted when empty. |
| `CE_EMBEDDER_MODEL` | required | Embedding model name. |
| `CE_EMBEDDER_DIMENSIONS` | required | Vector width produced by `CE_EMBEDDER_MODEL`. Changing it requires a Reindex. |

## Benchmark-driven development

Loom's retrieval quality is tracked by running it, unmodified, through its
public Ingest/Retrieve contract against published long-term-memory
benchmarks (LoCoMo, LongMemEval, BEAM), plus a Component Benchmark (LMEB)
that evaluates the Embedder in isolation. See `benchmark/` for the Harness
and datasets, and `CONTEXT-MAP.md` for how benchmarking relates to the
Engine.

## License

Loom is Fair Source, licensed under FSL-1.1-ALv2 (see `LICENSE.md`). Each
release automatically becomes available under Apache-2.0 on its second
anniversary (see `docs/adr/0010-loom-ships-under-fsl.md`).
