DO $$
DECLARE
  start_month DATE := (date_trunc('month', CURRENT_DATE) - INTERVAL '12 months')::DATE;
  end_month   DATE := (date_trunc('month', CURRENT_DATE) + INTERVAL '120 months')::DATE;
  from_month  DATE;
  to_month    DATE;
  part_name   TEXT;
BEGIN
  from_month := start_month;
  WHILE from_month < end_month LOOP
    to_month := (from_month + INTERVAL '1 month')::DATE;
    part_name := format('entries_y%sm%s', to_char(from_month, 'YYYY'), to_char(from_month, 'MM'));
    EXECUTE format(
      'CREATE TABLE IF NOT EXISTS %I PARTITION OF entries FOR VALUES FROM (%L) TO (%L);',
      part_name,
      from_month::TEXT,
      to_month::TEXT
    );
    from_month := to_month;
  END LOOP;
END $$;
