DO $$
DECLARE
  system_user_id UUID;
  system_acct_id UUID;
  alice_id UUID;
  bob_id UUID;
  alice_acct_id UUID;
  bob_acct_id UUID;
  tx_id UUID;
BEGIN
  IF EXISTS (SELECT 1 FROM users WHERE email = 'alice@example.com') THEN
    RAISE NOTICE 'seed already applied, skipping';
    RETURN;
  END IF;

  INSERT INTO users (email) VALUES ('system@localhost') RETURNING id INTO system_user_id;
  INSERT INTO accounts (owner_id, currency, purpose, is_system)
  VALUES (system_user_id, 'USD', 'available', TRUE)
  RETURNING id INTO system_acct_id;

  INSERT INTO users (email) VALUES ('alice@example.com') RETURNING id INTO alice_id;
  INSERT INTO users (email) VALUES ('bob@example.com') RETURNING id INTO bob_id;

  INSERT INTO accounts (owner_id, currency, purpose)
  VALUES (alice_id, 'USD', 'available') RETURNING id INTO alice_acct_id;
  INSERT INTO accounts (owner_id, currency, purpose)
  VALUES (alice_id, 'USD', 'escrow');
  INSERT INTO accounts (owner_id, currency, purpose)
  VALUES (bob_id, 'USD', 'available') RETURNING id INTO bob_acct_id;
  INSERT INTO accounts (owner_id, currency, purpose)
  VALUES (bob_id, 'USD', 'escrow');

  INSERT INTO transactions (type) VALUES ('deposit') RETURNING id INTO tx_id;
  INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, currency, status) VALUES
    (tx_id, system_acct_id, 'debit', 100000, 'USD', 'posted'),
    (tx_id, alice_acct_id, 'credit', 100000, 'USD', 'posted');

  INSERT INTO transactions (type) VALUES ('deposit') RETURNING id INTO tx_id;
  INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, currency, status) VALUES
    (tx_id, system_acct_id, 'debit', 100000, 'USD', 'posted'),
    (tx_id, bob_acct_id, 'credit', 100000, 'USD', 'posted');
END $$;