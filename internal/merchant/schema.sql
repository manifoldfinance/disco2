CREATE TABLE merchants (
    merchant_id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    category TEXT,
    logo_url TEXT,
    mcc INT,
    created_at TIMESTAMP DEFAULT now(),
    updated_at TIMESTAMP DEFAULT now()
);

-- Ensure uniqueness to avoid duplicates:
CREATE UNIQUE INDEX merchants_name_mcc_idx ON merchants( lower(name), coalesce(mcc,0) );
