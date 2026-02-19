ALTER TABLE accounts ADD CONSTRAINT currency_check CHECK (currency IN ('USD', 'EUR', 'GBP'));
