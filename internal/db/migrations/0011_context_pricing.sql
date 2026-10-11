ALTER TABLE charity_models ADD COLUMN context_pricing TEXT NOT NULL DEFAULT '{}' CHECK(length(context_pricing)<=2048 AND json_valid(context_pricing) AND json_type(context_pricing)='object');
ALTER TABLE charity_reservations ADD COLUMN context_pricing TEXT NOT NULL DEFAULT '{}' CHECK(length(context_pricing)<=2048 AND json_valid(context_pricing) AND json_type(context_pricing)='object');
