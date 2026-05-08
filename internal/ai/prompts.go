package ai

// Prompts are typed constants so they're easy to grep for, easy to test, and
// can't be accidentally shadowed at runtime. They are deliberately
// provider-agnostic: plain text in, plain text out, no Claude tool use, no
// OpenAI function calling, no JSON mode.

// PromptID lets call sites refer to a prompt by name in logs / metrics.
type PromptID string

const (
	PromptFixSystem      PromptID = "fix.system"
	PromptFixUser        PromptID = "fix.user"
	PromptFixBatchSystem PromptID = "fix.batch.system"
	PromptFixBatchUser   PromptID = "fix.batch.user"
	PromptSummarySystem  PromptID = "summary.system"
	PromptSummaryUser    PromptID = "summary.user"
)

// promptFixSystem frames the model as a Shopify SEO/GMC consultant whose only
// job is to produce a single rewrite. It explicitly forbids long preambles.
const promptFixSystem = `You are a Shopify-store consultant specializing in Google Merchant Center compliance and on-page SEO.
Your job: read one issue and propose a single, drop-in replacement value the merchant can paste back into Shopify.
Rules:
- Output ONLY the rewritten value. No preamble, no markdown, no quotes.
- Do not invent product details (specs, certifications, GTINs). If you can't tell, write a generic but accurate rewrite.
- Match the merchant's tone if a brand description is provided.
- Stay under 800 characters unless the issue is a long-form description.`

// promptFixUser is rendered with %s for: store context, check id, severity,
// title, detail, product title, product URL, evidence.
const promptFixUser = `Store context:
%s

Issue:
- check_id: %s
- severity: %s
- title: %s
- detail: %s
- product: %s (%s)
- evidence: %s

Suggest the rewritten value.`

// promptFixBatchSystem asks for one fix per issue, marked with the literal
// token "<<FIX N>>:" so the parser can split deterministically without
// relying on JSON.
const promptFixBatchSystem = `You are a Shopify-store consultant specializing in Google Merchant Center compliance and on-page SEO.
You will receive a numbered list of issues. For each, propose a single drop-in replacement value.
Rules:
- Output exactly one line per issue, using this format and no other:
    <<FIX 1>>: {rewritten value for issue 1}
    <<FIX 2>>: {rewritten value for issue 2}
    ... etc.
- Do not invent specs, certifications, or GTINs. Generic-but-accurate is fine.
- Keep each rewrite under 800 characters.
- No preamble, no markdown, no closing summary.`

const promptFixBatchUser = `Store context:
%s

Issues:
%s

Reply with one <<FIX N>> line per issue, in order, and nothing else.`

// promptSummarySystem keeps the executive summary tight and actionable.
const promptSummarySystem = `You are writing the executive summary of a Google Merchant Center compliance audit
for the store owner. Tone: direct, plain English, no marketing fluff.
Rules:
- Open with a one-sentence verdict (e.g. "This store is largely GMC-ready, with three blocking issues to fix first.").
- Then 3 to 5 bulleted next steps in priority order.
- Output format, exactly:
    SUMMARY: <verdict sentence>
    NEXT STEPS:
    - ...
    - ...
- No other text, no markdown headers, no closing remarks.`

const promptSummaryUser = `Store: %s (%s)
Score: %d / 100  ·  Risk: %s
Issue counts: critical=%d, error=%d, warning=%d, info=%d
Store context:
%s

Top issues to inform the prioritisation:
%s`

// Prompt is a small helper so callers can fetch the text via the typed ID.
func Prompt(id PromptID) string {
	switch id {
	case PromptFixSystem:
		return promptFixSystem
	case PromptFixUser:
		return promptFixUser
	case PromptFixBatchSystem:
		return promptFixBatchSystem
	case PromptFixBatchUser:
		return promptFixBatchUser
	case PromptSummarySystem:
		return promptSummarySystem
	case PromptSummaryUser:
		return promptSummaryUser
	}
	return ""
}
