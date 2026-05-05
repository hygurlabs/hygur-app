package mail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hygur/sidecar/internal/auth"
	"github.com/hygur/sidecar/internal/mail/gmail"
	"github.com/rs/zerolog"
)

// MigrationResult summarises what MigrateLegacyCredentials did during one
// boot. Useful for log lines and future telemetry.
type MigrationResult struct {
	ProtonMigrated      bool
	GmailMigrated       bool
	ProtonAccountID     string
	GmailAccountID      string
	KnowledgeItemsMoved int64
}

// MigrateLegacyCredentials reads the pre-multi-account credential files
// (mail:proton, mail:gmail + the connector-level mail credential) and writes
// equivalent MailAccount entries indexed by account_id. The legacy entries
// are NOT deleted so a user can roll back if anything goes wrong.
//
// For Gmail, the email address is resolved by issuing a single
// `users.getProfile` call. If that fails (network down, token revoked) we
// fall back to a stable placeholder "gmail-legacy" so the migration is
// idempotent and the user can still see the entry.
//
// If a sql.DB is provided, knowledge_items.content_id rows shaped like
// "mail:proton:..." and "mail:gmail:..." are rewritten to use the resolved
// account_id so the per-account count stays accurate post-migration.
func MigrateLegacyCredentials(ctx context.Context, store *auth.CredentialStore, db *sql.DB, logger zerolog.Logger) (MigrationResult, error) {
	res := MigrationResult{}
	if store == nil {
		return res, nil
	}

	// --- Proton ----------------------------------------------------------
	if username, password, err := store.GetMailCredential("proton"); err == nil && username != "" {
		accountID := username
		// Skip if already migrated (an account with this id exists).
		if _, gerr := store.GetMailAccountCredential(accountID); errors.Is(gerr, auth.ErrCredentialNotFound) {
			cred := auth.MailAccountCredential{
				AccountID: accountID,
				Provider:  "proton",
				Email:     accountID,
				Username:  username,
				Password:  password,
			}
			if serr := store.SaveMailAccountCredential(cred); serr != nil {
				logger.Warn().Err(serr).Msg("legacy proton migration: save failed")
			} else {
				res.ProtonMigrated = true
				res.ProtonAccountID = accountID
				logger.Info().Str("account", accountID).Msg("legacy proton credential migrated")
				if db != nil {
					if n, mErr := rewriteContentIDs(ctx, db, "proton", accountID); mErr != nil {
						logger.Warn().Err(mErr).Msg("legacy proton content_id rewrite failed")
					} else {
						res.KnowledgeItemsMoved += n
					}
				}
			}
		}
	}

	// --- Gmail (connector-level credential is the source of truth since 2025) ----
	gmailCreds, _ := store.GetConnectorCredential("mail")
	if gmailCreds == nil {
		// Fall back to the legacy per-source store.
		if rt, cid, sec, err := store.GetGmailCredential(); err == nil && rt != "" {
			gmailCreds = map[string]string{"refresh_token": rt, "client_id": cid, "client_secret": sec}
		}
	}
	if rt := gmailCreds["refresh_token"]; rt != "" {
		clientID := gmailCreds["client_id"]
		clientSecret := gmailCreds["client_secret"]
		email := resolveGmailEmail(ctx, clientID, clientSecret, rt, logger)
		accountID := email
		if accountID == "" {
			accountID = "gmail-legacy"
		}
		if _, gerr := store.GetMailAccountCredential(accountID); errors.Is(gerr, auth.ErrCredentialNotFound) {
			cred := auth.MailAccountCredential{
				AccountID:    accountID,
				Provider:     "gmail",
				Email:        email,
				RefreshToken: rt,
				ClientID:     clientID,
				ClientSecret: clientSecret,
			}
			if serr := store.SaveMailAccountCredential(cred); serr != nil {
				logger.Warn().Err(serr).Msg("legacy gmail migration: save failed")
			} else {
				res.GmailMigrated = true
				res.GmailAccountID = accountID
				logger.Info().Str("account", accountID).Msg("legacy gmail credential migrated")
				if db != nil {
					if n, mErr := rewriteContentIDs(ctx, db, "gmail", accountID); mErr != nil {
						logger.Warn().Err(mErr).Msg("legacy gmail content_id rewrite failed")
					} else {
						res.KnowledgeItemsMoved += n
					}
				}
			}
		}
	}

	return res, nil
}

// resolveGmailEmail performs a one-shot users.getProfile call to extract the
// authenticated email. Returns "" if the call fails — the caller falls back
// to a placeholder account id.
func resolveGmailEmail(ctx context.Context, clientID, clientSecret, refreshToken string, logger zerolog.Logger) string {
	if clientID == "" || refreshToken == "" {
		return ""
	}
	conn := gmail.NewGmailConnector(clientID, clientSecret, "urn:ietf:wg:oauth:2.0:oob")
	conn.SetRefreshToken(refreshToken)

	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := conn.Connect(cctx); err != nil {
		logger.Warn().Err(err).Msg("gmail email resolution: connect failed")
		return ""
	}
	email, err := conn.GetProfileEmail(cctx)
	if err != nil {
		logger.Warn().Err(err).Msg("gmail email resolution: getProfile failed")
		return ""
	}
	return email
}

// rewriteContentIDs updates knowledge_items where the content_id matches the
// pre-migration "mail:{provider}:..." prefix to the new "mail:{accountID}:..."
// form. Returns the number of rows affected.
func rewriteContentIDs(ctx context.Context, db *sql.DB, provider, accountID string) (int64, error) {
	oldPrefix := "mail:" + provider + ":"
	newPrefix := "mail:" + accountID + ":"
	if oldPrefix == newPrefix {
		return 0, nil
	}
	res, err := db.ExecContext(ctx, `
		UPDATE knowledge_items
		SET content_id = ? || SUBSTR(content_id, LENGTH(?) + 1),
		    updated_at = CURRENT_TIMESTAMP
		WHERE source_type = 'email' AND content_id LIKE ? || '%'
	`, newPrefix, oldPrefix, oldPrefix)
	if err != nil {
		return 0, fmt.Errorf("rewriting content_ids: %w", err)
	}
	rows, _ := res.RowsAffected()
	return rows, nil
}
