package stale // want `stale e2eutilallowlist entry Version414 in Existing` `stale e2eutilallowlist entry GetConfig in Removed`
import "github.com/openshift/hypershift/test/e2e/util"

func Existing() { _, _ = util.GetConfig() }
