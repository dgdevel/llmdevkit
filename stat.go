package main

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/sys/unix"
)

func humanSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func lookupUser(uid uint32) string {
	u, err := user.LookupId(strconv.Itoa(int(uid)))
	if err != nil {
		return strconv.Itoa(int(uid))
	}
	return u.Username
}

func lookupGroup(gid uint32) string {
	g, err := user.LookupGroupId(strconv.Itoa(int(gid)))
	if err != nil {
		return strconv.Itoa(int(gid))
	}
	return g.Name
}

func userPerms(mode uint32, uid, gid uint32) string {
	currUid := os.Getuid()
	currGid := os.Getgid()
	groups, _ := os.Getgroups()

	var bits uint32
	if currUid == int(uid) {
		bits = (mode >> 6) & 7
	} else if currGid == int(gid) || hasGroup(groups, int(gid)) {
		bits = (mode >> 3) & 7
	} else {
		bits = mode & 7
	}

	var perms []string
	if bits&4 != 0 {
		perms = append(perms, "read")
	}
	if bits&2 != 0 {
		perms = append(perms, "write")
	}
	if bits&1 != 0 {
		perms = append(perms, "execute")
	}
	if len(perms) == 0 {
		return "none"
	}
	return strings.Join(perms, ",")
}

func hasGroup(groups []int, gid int) bool {
	for _, g := range groups {
		if g == gid {
			return true
		}
	}
	return false
}

func formatTime(ts syscall.Timespec) string {
	return time.Unix(ts.Sec, ts.Nsec).Format("2006-01-02T15:04:05")
}

func getBirthTime(abs string, st *syscall.Stat_t) string {
	var stx unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, abs, 0, unix.STATX_BTIME, &stx)
	if err == nil && stx.Mask&unix.STATX_BTIME != 0 {
		return time.Unix(int64(stx.Btime.Sec), int64(stx.Btime.Nsec)).Format("2006-01-02T15:04:05")
	}
	return formatTime(st.Ctim)
}

func statHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	abs, err := resolve(p)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if isConfigPath(abs) || isIgnored(abs) {
		return mcp.NewToolResultError("access denied"), nil
	}
	var st syscall.Stat_t
	if err := syscall.Stat(abs, &st); err != nil {
		return mcp.NewToolResultError(maskPath(err.Error())), nil
	}

	typ := "file"
	if st.Mode&syscall.S_IFMT == syscall.S_IFDIR {
		typ = "directory"
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "Type: %s\n", typ)
	fmt.Fprintf(&buf, "Size: %d, %s\n", st.Size, humanSize(st.Size))
	fmt.Fprintf(&buf, "Permissions: %s\n", userPerms(st.Mode, st.Uid, st.Gid))
	fmt.Fprintf(&buf, "Owner: %s(uid=%d)\n", lookupUser(st.Uid), st.Uid)
	fmt.Fprintf(&buf, "Group: %s(gid=%d)\n", lookupGroup(st.Gid), st.Gid)
	fmt.Fprintf(&buf, "Access: %s\n", formatTime(st.Atim))
	fmt.Fprintf(&buf, "Modify: %s\n", formatTime(st.Mtim))
	fmt.Fprintf(&buf, "Change: %s\n", formatTime(st.Ctim))
	fmt.Fprintf(&buf, "Birth: %s\n", getBirthTime(abs, &st))
	return mcp.NewToolResultText(buf.String()), nil
}
