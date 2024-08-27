# Wrapped pgx

Wrapped pgx is a simple wrap on the PostgreSQL driver library [pgx](https://github.com/jackc/pgx).
It is used by [sqlc](https://github.com/Stumble/sqlc), providing telementry for generated code.

Components:

- **Pool**: a wrapper of pgxpool.Pool. It manages a set of connection pools, including a primary pool 
  and a set of replica pools. We assume replica pools are heterogeneous read-only replicas, meaning
  some replicas can be a partial copy of the primary database, using logical replication.
- **WConn**: a connection wrapper, implementing "WGConn".
- **WTx**: a transaction wrapper, implementing "WGConn".
