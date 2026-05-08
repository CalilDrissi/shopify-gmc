-- Extend the plan_tier enum to match the spec.
-- Existing rows on `pro` keep working — plan_limits.go maps "starter" → "pro"
-- so legacy data is honoured, but new sales for the Starter SKU land as
-- "starter" once code/seed data is updated.
--
-- ALTER TYPE … ADD VALUE must run outside a transaction; golang-migrate
-- already runs each migration file in its own transaction, but the enum
-- alterations themselves are auto-committed.
ALTER TYPE plan_tier ADD VALUE IF NOT EXISTS 'starter';
ALTER TYPE plan_tier ADD VALUE IF NOT EXISTS 'growth';
