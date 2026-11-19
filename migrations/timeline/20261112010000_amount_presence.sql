-- Timeline amount presence is separate from its minor-unit value so NULL/blank
-- remains distinct from an explicitly entered 0.00 amount.
ALTER TABLE "timeline_entries" ADD COLUMN "amount_present" boolean NOT NULL DEFAULT false;
