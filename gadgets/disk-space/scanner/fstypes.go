package main

// Filesystem types by statfs magic number, and which ones a scan descends into.

type fstat struct {
	dev, ino, nlink uint64
	size, blocks    int64 // blocks: bytes actually allocated (st_blocks × 512)
}

// diskFS: real storage. Scanned when a mount point leads to one.
var diskFS = map[string]bool{
	"ext4": true, "btrfs": true, "xfs": true, "vfat": true, "exfat": true, "ntfs": true,
	"ntfs3": true, "f2fs": true, "zfs": true, "jfs": true, "reiserfs": true, "hfsplus": true,
}

// kernelFS: not files at all (/proc, /sys …). Everything else that isn't diskFS (tmpfs, overlay
// layers of containers, network shares, FUSE) is skipped as "otherfs" unless allowed with ALLOW_FS.
var kernelFS = map[string]bool{
	"proc": true, "sysfs": true, "devpts": true, "cgroup": true, "cgroup2": true, "debugfs": true,
	"tracefs": true, "securityfs": true, "bpf": true, "pstore": true, "configfs": true,
	"mqueue": true, "nsfs": true, "fusectl": true, "autofs": true, "binfmt_misc": true,
	"efivarfs": true, "hugetlbfs": true,
}

var fsNames = map[uint64]string{
	0xEF53: "ext4", 0x9123683E: "btrfs", 0x58465342: "xfs", 0x4d44: "vfat", 0x2011BAB0: "exfat",
	0x5346544e: "ntfs", 0x7366746e: "ntfs3", 0xF2F52010: "f2fs", 0x2fc12fc1: "zfs",
	0x3153464a: "jfs", 0x52654973: "reiserfs", 0x482b: "hfsplus",
	0x01021994: "tmpfs", 0x858458f6: "ramfs", 0x794c7630: "overlay", 0x6969: "nfs",
	0xFF534D42: "cifs", 0xFE534D42: "smb2", 0x65735546: "fuse", 0x9660: "iso9660",
	0x73717368: "squashfs",
	0x9fa0: "proc", 0x62656572: "sysfs", 0x1cd1: "devpts", 0x27e0eb: "cgroup",
	0x63677270: "cgroup2", 0x64626720: "debugfs", 0x74726163: "tracefs", 0x73636673: "securityfs",
	0xcafe4a11: "bpf", 0x6165676C: "pstore", 0x62656570: "configfs", 0x19800202: "mqueue",
	0x6e736673: "nsfs", 0x65735543: "fusectl", 0x0187: "autofs", 0x42494e4d: "binfmt_misc",
	0xde5e81e4: "efivarfs", 0x958458f6: "hugetlbfs",
}
