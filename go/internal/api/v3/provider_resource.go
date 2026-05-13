package v3

// providerResourceCore carries the inherited fields from
// `src/Sonarr.Api.V3/ProviderResource.cs` that every provider list
// endpoint emits (Notifications, Indexers, ImportLists,
// DownloadClients, Metadata). Each concrete resource embeds this
// core and adds its own per-controller fields.
//
// Empty-DB lists never trigger the populated-row mapper, so the
// minimal-fields stub below is byte-eq-safe today. Populated rows
// will land their full wire shape when the schema-builder port adds
// Fields/Presets/Message support — DEFERRED, flagged in STATUS.
//
// Field-source mapping (C# → Go), shared base:
//
//	id                 → ProviderDefinition.Id
//	name               → ProviderDefinition.Name
//	implementation     → ProviderDefinition.Implementation
//	implementationName → ProviderDefinition.ImplementationName
//	configContract     → ProviderDefinition.ConfigContract
//	tags               → ProviderDefinition.Tags (HashSet<int> → []int{})
//	fields             → SchemaBuilder.ToSchema(Settings) — DEFERRED
//	infoLink           → ProviderDefinition.InfoLink — DEFERRED
//	message            → ProviderDefinition.Message — DEFERRED
//	presets            → ProviderDefinition.Presets — DEFERRED
type providerResourceCore struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Implementation     string `json:"implementation"`
	ImplementationName string `json:"implementationName"`
	ConfigContract     string `json:"configContract"`
	Tags               []int  `json:"tags"`
}
