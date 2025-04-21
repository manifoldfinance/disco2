CREATE TABLE accounts (
    account_id UUID PRIMARY KEY,
    balance BIGINT NOT NULL, -- in cents
    updated_at TIMESTAMP
);

-- Optional ledger_entries table for audit history
-- CREATE TABLE ledger_entries (
--     entry_id serial PRIMARY KEY,
--     account_id UUID NOT NULL,
--     amount BIGINT NOT NULL, -- positive for credit, negative for debit
--     description TEXT,
--     created_at TIMESTAMP DEFAULT now()
-- );
