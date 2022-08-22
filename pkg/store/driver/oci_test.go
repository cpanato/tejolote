/*
Copyright 2022 Adolfo García Veytia

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package driver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOCISnapshot(t *testing.T) {
	oci, err := NewOCI("oci://ghcr.io/uservers/miniprow/miniprow")
	require.NoError(t, err)
	require.Equal(t, "miniprow", oci.Image)
	require.Equal(t, "ghcr.io/uservers/miniprow", oci.Repository)

	snap, err := oci.Snap()
	require.NoError(t, err)
	require.Len(t, *snap, 5)
}
