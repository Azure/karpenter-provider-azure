# SecurityPatch node image selection

Managed Node Auto Provisioning can use the AKS `SecurityPatch` node OS upgrade channel when it
provisions through the AKS Machine API. Karpenter requests the standard node image catalog together
with provisionable SecurityPatch images and records one preferred image for each supported OS and
SKU in `AKSNodeClass.status.images`.

When both variants are available, Karpenter prefers the SecurityPatch image. It compares the base
image version first and the security patch date second. If no provisionable SecurityPatch image
exists for an OS and SKU, Karpenter retains the standard image so image families without SecurityPatch
coverage can continue to provision.

Image updates keep the existing NodeOS maintenance-window behavior. A newly discovered preferred
image updates `status.images` during the configured window, and the existing image drift check
replaces nodes whose image is no longer in that set. Turning the cluster away from SecurityPatch
immediately restores standard images because the service only accepts SecurityPatch image versions
while that channel is active.

Explicit `spec.versions.nodeImageVersion` pins and rollback metadata use standard base image
versions. Pinning therefore selects a standard image rather than applying a SecurityPatch version
across unrelated OS and SKU variants.

This behavior selects prepatched images for new or replaced nodes. Applying patches in place to
existing nodes is separate from image selection.
