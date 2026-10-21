-- Operation receipts prevent repeated counter changes; this is not a task queue.
\connect loyalties

CREATE TABLE IF NOT EXISTS loyalty_updates (
    operation_key VARCHAR(128) PRIMARY KEY,
    username VARCHAR(80) NOT NULL,
    change INT NOT NULL CHECK (change IN (-1, 1))
);

GRANT SELECT, INSERT ON loyalty_updates TO program;
