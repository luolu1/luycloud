# Releases

## v1.1.1

Release date: 2026-09-30

#### Added

- Added the VM creation time to the VM detail page under **System Information**.
- The creation time is displayed in the basic configuration section and uses the existing VM detail API field.

#### Fixed

- After a successful cross-node migration, the source VM definition is now moved to the recycle bin instead of remaining as an active source-side copy.
- Source VM disks remain in place and can be recovered through the recycle bin.
- A source recycle-bin failure is reported as a migration warning without incorrectly marking an already completed migration as failed.
- Fixed migration failures caused by CD-ROM ISO files that exist on the source node but are unavailable on the target node.

#### Documentation

- Updated the sponsor attribution in `README.md` to credit ForZTN.
