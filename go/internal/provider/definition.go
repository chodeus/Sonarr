package provider

// This file ports the runtime ProviderDefinition + ProviderMessage types
// from src/NzbDrone.Core/ThingiProvider/{ProviderDefinition.cs,ProviderMessage.cs}.
//
// The persisted row shapes (indexers.Indexer, downloadclients.DownloadClient,
// notifications.Notification, importlists.ImportList, metadatafiles.Metadata)
// already exist as Phase-1 row types — those carry the column-level fields.
// This package provides the family-agnostic *runtime* projection that
// service code passes around once a row has been hydrated through the
// (Implementation, ConfigContract) registry.
//
// Why split row types vs. Definition: the C# code threads
// ProviderDefinition through every IProvider method (Test, RequestAction,
// DefaultDefinitions). The persisted row types in Go carry storage-layer
// concerns (nullable columns, tags JSON encoding, etc.) that we don't
// want bleeding into provider plumbing. Definition is the narrow,
// service-layer view.

// Definition is the family-agnostic runtime view of a provider row.
// C# cite: src/NzbDrone.Core/ThingiProvider/ProviderDefinition.cs:8-45.
//
// The C# type is an abstract base extended per-family
// (IndexerDefinition adds Protocol/Priority, DownloadClientDefinition
// adds Removed/RemoveCompletedDownloads, etc.). The Go port keeps the
// shared fields here and lets per-family runtime types embed Definition
// directly (no inheritance needed — composition via embedding).
type Definition struct {
	// ID is the row primary key. Zero for definitions not yet persisted
	// (e.g. DefaultDefinitions output before save).
	ID int

	// Name is the user-visible label. Defaults to the provider class name
	// (Implementation) when DefaultDefinitions is invoked.
	Name string

	// Implementation is the C# class name string the row carries forever.
	// Identifier registry key together with ConfigContract.
	Implementation string

	// ConfigContract is the C# Settings class name string. Distinguishes
	// providers that share a settings shape (e.g. Vuze and Transmission
	// both use TransmissionSettings).
	ConfigContract string

	// Enable mirrors the C# `Enable` getter (ProviderDefinition.cs:25).
	// Per-family definitions add EnableRss / EnableAutomaticSearch /
	// EnableInteractiveSearch flags that override this for sub-modes.
	Enable bool

	// Settings is the typed Config instance. Loaded by Factory.LoadDefinition
	// (or set directly by service code during creation/edit). nil when
	// the row has not been hydrated through the Factory yet.
	Settings Config

	// SettingsRaw carries the raw JSON bytes from the row's Settings
	// column. Preserved so byte-for-byte round-trip with C# user
	// databases is maintained even when a class rename leaves the
	// runtime constructor missing — service code can still surface the
	// row to the user with a "missing implementation" Message rather
	// than dropping it.
	SettingsRaw string

	// Message is the optional status banner shown next to the provider
	// in the UI. Mirrors ProviderDefinition.cs:28.
	Message *Message

	// Tags is the row's per-tag association list. Stored as JSON []int
	// in the column; service code threads the int IDs through tagsSvc
	// for filtering. Empty slice (not nil) is the canonical zero value
	// for round-trip parity with the C# `HashSet<int>`.
	Tags []int
}

// Message is the status banner. C# cite:
// src/NzbDrone.Core/ThingiProvider/ProviderMessage.cs:3-13.
type Message struct {
	Text string
	Type MessageType
}

// MessageType mirrors the C# enum at ProviderMessage.cs:15-20.
type MessageType int

// Message severities. The integer values mirror the C# enum order
// because they cross the API/v3 + API/v5 boundaries as ints.
const (
	MessageInfo    MessageType = iota // 0
	MessageWarning                    // 1
	MessageError                      // 2
)

// String returns the C# enum-ish name for diagnostics and JSON output.
func (m MessageType) String() string {
	switch m {
	case MessageInfo:
		return "info"
	case MessageWarning:
		return "warning"
	case MessageError:
		return "error"
	default:
		return "unknown"
	}
}

// NewInfoMessage / NewWarningMessage / NewErrorMessage are tiny constructors
// matching the C# `new ProviderMessage(text, type)` call sites. Provided
// purely to keep call sites readable — `provider.NewWarningMessage("...")`
// reads better than `&provider.Message{Text: "...", Type: provider.MessageWarning}`.
func NewInfoMessage(text string) *Message {
	return &Message{Text: text, Type: MessageInfo}
}

// NewWarningMessage builds a warning-severity Message.
func NewWarningMessage(text string) *Message {
	return &Message{Text: text, Type: MessageWarning}
}

// NewErrorMessage builds an error-severity Message.
func NewErrorMessage(text string) *Message {
	return &Message{Text: text, Type: MessageError}
}
