// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Build when CGO is enabled but the target OS or architecture are not supported
//go:build without_ddwaf
// +build without_ddwaf

package bindings

var disabledReason = "the go build tag without_ddwaf was used to disable the waf"