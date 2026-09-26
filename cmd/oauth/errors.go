package main

import "errors"

var (
	errBrowserLauncherNotFound   = errors.New("no supported browser launcher found")
	errCredentialFileInvalidJSON = errors.New("credential file is invalid JSON")
	errCredentialFilePathEmpty   = errors.New("credential file path is empty")
	errCredentialFileSaveInvalid = errors.New("credential file path and valid JSON are required")
	errCredentialProviderMissing = errors.New("credential file has no provider; pass --provider")
	errUsage                     = errors.New("invalid command usage")
)
