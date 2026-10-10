package app

// These are storage/wire-format identifiers, not product branding. Changing
// them would orphan existing databases, encrypted device keys or sessions.
const databaseName = "microseg.db"
const legacyCredentialAAD = "microseg-device-v1"
const sessionCookieName = "microseg_session"
