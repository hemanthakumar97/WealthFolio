// Package domain holds canonical enum values shared across handlers, services, and parsers.
package domain

// Asset types — must match the CHECK constraint on instruments.asset_type.
const (
	AssetTypeMF     = "MF"
	AssetTypeETF    = "ETF"
	AssetTypeStock  = "STOCK"
	AssetTypeBond   = "BOND"
	AssetTypeMetal  = "METAL"
	AssetTypeOther  = "OTHER"
	AssetTypeUSFund = "US_FUND"
)

// Currencies — must match the CHECK constraint on instruments.currency.
const (
	CurrencyINR = "INR"
	CurrencyUSD = "USD"
)

// Platforms — must match the CHECK constraint on transactions.platform.
const (
	PlatformZerodha  = "ZERODHA"
	PlatformGroww    = "GROWW"
	PlatformINDMoney = "INDMONEY"
	PlatformManual   = "MANUAL"
)

// Transaction types — must match the CHECK constraint on transactions.transaction_type.
const (
	TxBuy       = "BUY"
	TxSell      = "SELL"
	TxSwitchIn  = "SWITCH_IN"
	TxSwitchOut = "SWITCH_OUT"
	TxDividend  = "DIVIDEND"
	TxBonus     = "BONUS"
	TxSplit     = "SPLIT"
)

// Upload statuses — must match the CHECK constraint on upload_history.status.
const (
	UploadPending    = "PENDING"
	UploadProcessing = "PROCESSING"
	UploadCompleted  = "COMPLETED"
	UploadFailed     = "FAILED"
	UploadPartial    = "PARTIAL"
)

// Import-log statuses.
const (
	ImportImported  = "IMPORTED"
	ImportDuplicate = "DUPLICATE"
	ImportError     = "ERROR"
)

// Allocation categories — must match the CHECK constraint on instrument_allocations and category_allocations.
const (
	AllocEquity   = "EQUITY"
	AllocGold     = "GOLD"
	AllocDebt     = "DEBT"
	AllocUSEquity = "US_EQUITY"
	AllocOthers   = "OTHERS"
)
