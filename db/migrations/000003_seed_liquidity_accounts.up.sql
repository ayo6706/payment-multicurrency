-- System User with fixed UUID
INSERT INTO users (id, username, email, role, created_at)
VALUES ('11111111-1111-1111-1111-111111111111', 'system_liquidity', 'system@grey.finance', 'system', NOW())
ON CONFLICT (id) DO NOTHING;

-- System Accounts with fixed UUIDs
INSERT INTO accounts (id, user_id, currency, balance, created_at)
VALUES
('22222222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111', 'USD', 0, NOW()),
('33333333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', 'EUR', 0, NOW()),
('44444444-4444-4444-4444-444444444444', '11111111-1111-1111-1111-111111111111', 'GBP', 0, NOW())
ON CONFLICT (id) DO NOTHING;
