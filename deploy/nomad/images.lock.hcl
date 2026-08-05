# Immutable image authority for ultracore (product of aleksclark/ultralogical).
# Updated by the release workflow after GHCR push. Deployed jobspec must use
# the digest form, never floating :latest as authority.
#
# image_repository = "ghcr.io/aleksclark/ultracore"
# image_tag        = "0.2.0"
# image_digest     = ""  # filled after first immutable GHCR publish
# note: until digest is published, fleet nodes use the preloaded local tag.

locals {
  ultracore_image_repo   = "ghcr.io/aleksclark/ultracore"
  ultracore_image_tag    = "0.2.0"
  ultracore_image_digest = ""
}
