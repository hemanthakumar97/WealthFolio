-- +goose Up

-- Rename the GOLD allocation category to METALS (it holds gold AND silver).
-- Drop the CHECK constraints, migrate existing rows, then re-add with METALS.
ALTER TABLE instrument_allocations DROP CONSTRAINT instrument_allocations_alloc_category_check;
ALTER TABLE category_allocations   DROP CONSTRAINT category_allocations_alloc_category_check;

UPDATE instrument_allocations SET alloc_category = 'METALS' WHERE alloc_category = 'GOLD';
UPDATE category_allocations   SET alloc_category = 'METALS' WHERE alloc_category = 'GOLD';

ALTER TABLE instrument_allocations ADD CONSTRAINT instrument_allocations_alloc_category_check
    CHECK (alloc_category IN ('EQUITY','METALS','DEBT','US_EQUITY','OTHERS'));
ALTER TABLE category_allocations ADD CONSTRAINT category_allocations_alloc_category_check
    CHECK (alloc_category IN ('EQUITY','METALS','DEBT','US_EQUITY','OTHERS'));

-- Keep the editable AI prompt in sync so the model emits METALS, not GOLD.
UPDATE ai_prompts SET content = replace(content, 'GOLD', 'METALS') WHERE key = 'allocation_suggest';

-- +goose Down

ALTER TABLE instrument_allocations DROP CONSTRAINT instrument_allocations_alloc_category_check;
ALTER TABLE category_allocations   DROP CONSTRAINT category_allocations_alloc_category_check;

UPDATE instrument_allocations SET alloc_category = 'GOLD' WHERE alloc_category = 'METALS';
UPDATE category_allocations   SET alloc_category = 'GOLD' WHERE alloc_category = 'METALS';

ALTER TABLE instrument_allocations ADD CONSTRAINT instrument_allocations_alloc_category_check
    CHECK (alloc_category IN ('EQUITY','GOLD','DEBT','US_EQUITY','OTHERS'));
ALTER TABLE category_allocations ADD CONSTRAINT category_allocations_alloc_category_check
    CHECK (alloc_category IN ('EQUITY','GOLD','DEBT','US_EQUITY','OTHERS'));

UPDATE ai_prompts SET content = replace(content, 'METALS', 'GOLD') WHERE key = 'allocation_suggest';
