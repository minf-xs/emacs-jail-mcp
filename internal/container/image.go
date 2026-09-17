// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

package container

import (
	_ "embed"
)

//go:embed Containerfile
var defaultContainerfile []byte
