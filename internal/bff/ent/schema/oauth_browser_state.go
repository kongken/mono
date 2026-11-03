package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// OAuthBrowserState holds a single browser authorization. IDs are references,
// never credentials; continuing it also requires the generation-bound Cookie.
type OAuthBrowserState struct{ ent.Schema }

func (OAuthBrowserState) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "oauth_browser_states", Checks: map[string]string{
		"oauth_state_provider":     "provider IN ('google','github')",
		"oauth_state_intent":       "intent IN ('login','link')",
		"oauth_state_status":       "result_status IN ('issued','exchanging','registration_pending','link_pending','registering','authenticated','failed')",
		"oauth_state_binding":      "octet_length(context_hash) = 32 AND length(state_hash) = 64 AND length(browser_binding_hash) = 64 AND length(nonce_hash) = 64 AND length(initiating_token_hash) = 64",
		"oauth_state_generation":   "browser_generation >= 0 AND provider_config_version > 0",
		"oauth_state_link_session": "intent <> 'link' OR (session_id IS NOT NULL AND user_id IS NOT NULL AND auth_version IS NOT NULL AND auth_version > 0)",
	}}}
}
func (OAuthBrowserState) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("usersrv_challenge_id").Unique().Immutable(),
		field.String("state_hash").Unique().Sensitive().Immutable(),
		field.Enum("provider").Values("google", "github").Immutable(),
		field.Enum("intent").Values("login", "link").Immutable(),
		field.String("browser_binding_hash").Sensitive().Immutable(),
		field.String("browser_context_id").Immutable(),
		field.Int64("browser_generation").Immutable(),
		field.String("initiating_token_hash").Sensitive().Immutable(),
		field.String("session_id").Optional().Nillable().Immutable(),
		field.String("user_id").Optional().Nillable().Immutable(),
		field.Int64("auth_version").Optional().Nillable().Immutable(),
		field.Int64("provider_config_version").Positive().Immutable(),
		field.Bytes("context_hash").Sensitive().Immutable(),
		field.String("nonce_hash").Sensitive().Immutable(),
		field.String("key_id").Immutable(),
		field.Bytes("pkce_verifier_ciphertext").Sensitive().Optional(),
		field.String("return_app_id").Immutable(),
		field.String("return_path").Immutable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("expires_at").Immutable(),
		field.Time("consumed_at").Optional().Nillable(),
		field.String("result_status").Default("issued"),
		field.Time("final_claimed_at").Optional().Nillable(),
		field.String("final_mutation_id").Optional(),
		field.String("final_request_hash").Optional().Sensitive(),
		field.Time("confirmation_expires_at").Optional().Nillable(),
	}
}
func (OAuthBrowserState) Indexes() []ent.Index { return []ent.Index{index.Fields("expires_at")} }

// BrowserContext serializes account changes across tabs. A delayed Set-Cookie
// from an older generation cannot authenticate as an earlier account.
type BrowserContext struct{ ent.Schema }

func (BrowserContext) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auth_browser_contexts"}}
}
func (BrowserContext) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Sensitive().Immutable(),
		field.Int64("generation").Default(0),
		field.Time("expires_at"),
	}
}
func (BrowserContext) Indexes() []ent.Index { return []ent.Index{index.Fields("expires_at")} }
