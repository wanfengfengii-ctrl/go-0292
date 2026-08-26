-- 0001_init.sql: initial schema for the UHPC wet-joint durable snapshot store.
-- The snapshot table holds a single immutable current aggregate snapshot
-- (schema version, committed sequence list, digest and JSON state blob). The
-- domain_event table records each committed transaction event in the same
-- transaction as its snapshot so restart recovery can re-validate the event
-- sequence against the persisted snapshot.

CREATE TABLE IF NOT EXISTS snapshot (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    schema_version INTEGER NOT NULL,
    sequences      TEXT    NOT NULL,
    digest         TEXT    NOT NULL,
    state          BLOB    NOT NULL
);

CREATE TABLE IF NOT EXISTS domain_event (
    seq        INTEGER PRIMARY KEY,
    event_type TEXT    NOT NULL,
    digest     TEXT    NOT NULL
);
