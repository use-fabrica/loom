# Context Map

## Contexts

- [Engine](./CONTEXT.md): Loom itself — the self-hostable context engine that ingests conversational history and retrieves context on demand
- [Benchmarking](./benchmark/CONTEXT.md): measures the Engine against published long-term-memory benchmarks

## Relationships

- **Benchmarking → Engine**: the Harness drives the Engine black-box through its public Ingest/Retrieve contract, exactly as a self-hosted client would. Benchmarking never reaches into Engine internals; the one sanctioned exception is the Component Benchmark (LMEB), which evaluates the embedding layer in isolation.
