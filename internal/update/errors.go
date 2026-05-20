package update

// Sudo (R4)
const (
	ErrSudoUnavailable = "sudo_unavailable"
	ErrSudoLost        = "sudo_lost"
)

// URL validation (R6)
const (
	ErrURLCredentialsPresent  = "url_credentials_present"
	ErrURLSchemeNotHTTPS      = "url_scheme_not_https"
	ErrURLHostNotAllowed      = "url_host_not_allowed"
	ErrURLResolvesPrivateIP   = "url_resolves_private_ip"
	ErrAssetNotFoundForOSArch = "asset_not_found_for_platform"
)

// Download (R5)
const (
	ErrDownloadHTTPStatus = "download_http_status"
	ErrDownloadIOFailed   = "download_io_failed"
	ErrDownloadTimeout    = "download_timeout"
	ErrDownloadDiskFull   = "download_disk_full"
)

// Verify (R1, R2)
const (
	ErrTempChmodFailed    = "temp_chmod_failed"
	ErrVerifySizeOOR      = "verify_size_out_of_range"
	ErrVerifyChecksum     = "verify_checksum_mismatch"
	ErrVerifyArchMismatch = "verify_arch_mismatch"
	ErrVerifyNotELF       = "verify_not_elf"
)

// Install (R3)
const (
	ErrInstallStopFailed    = "install_stop_failed"
	ErrInstallReplaceFailed = "install_replace_failed"
	ErrInstallChownFailed   = "install_chown_failed"
	ErrInstallStartFailed   = "install_start_failed"
)

// Concurrency (R10)
const ErrUpdateAlreadyRunning = "update_already_running"
