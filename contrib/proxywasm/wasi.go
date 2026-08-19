package proxywasm

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"log/slog"
	"time"

	"github.com/tetratelabs/wazero/api"
)

// wasi.go 实现最小 WASI snapshot preview1 子集。
// Proxy-Wasm 插件(尤其 proxy-wasm-go-sdk 编译的)依赖部分 WASI 函数。

// registerWASI 注册最小 WASI 函数到 wasi_snapshot_preview1 模块。
func registerWASI(b *hostModuleBuilder, logger *slog.Logger) {
	// fd_write(fd, iovs_ptr, iovs_len, nwritten_ptr) → errno
	// stdout(1) / stderr(2) → slog 输出
	b.export("fd_write", func(ctx context.Context, mod api.Module, fd, iovsPtr, iovsLen, nwrittenPtr uint32) uint32 {
		mem := mod.Memory()
		if mem == nil {
			return 8 // EBADF
		}
		var totalWritten uint32
		for i := uint32(0); i < iovsLen; i++ {
			iovBase, _ := mem.ReadUint32Le(iovsPtr + i*8)
			iovLen, _ := mem.ReadUint32Le(iovsPtr + i*8 + 4)
			if iovLen == 0 {
				continue
			}
			data, ok := mem.Read(iovBase, iovLen)
			if !ok {
				continue
			}
			totalWritten += iovLen
			if logger != nil {
				msg := string(data)
				if fd == 2 {
					logger.Warn(msg, slog.String("source", "wasm-stderr"))
				} else {
					logger.Info(msg, slog.String("source", "wasm-stdout"))
				}
			}
		}
		if nwrittenPtr != 0 {
			buf := make([]byte, 4)
			binary.LittleEndian.PutUint32(buf, totalWritten)
			mem.Write(nwrittenPtr, buf)
		}
		return 0 // success
	})

	// clock_time_get(clock_id, precision, time_ptr) → errno
	// clock_id: 0=realtime, 1=monotonic
	b.export("clock_time_get", func(ctx context.Context, mod api.Module, clockID uint32, precision uint64, timePtr uint32) uint32 {
		mem := mod.Memory()
		if mem == nil {
			return 8
		}
		var ns uint64
		switch clockID {
		case 0: // realtime
			ns = uint64(time.Now().UnixNano())
		case 1: // monotonic
			ns = uint64(time.Now().UnixNano())
		default:
			return 28 // EINVAL
		}
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, ns)
		if !mem.Write(timePtr, buf) {
			return 21 // EFAULT
		}
		return 0
	})

	// random_get(buf_ptr, buf_len) → errno
	b.export("random_get", func(ctx context.Context, mod api.Module, bufPtr, bufLen uint32) uint32 {
		mem := mod.Memory()
		if mem == nil {
			return 8
		}
		buf := make([]byte, bufLen)
		if _, err := rand.Read(buf); err != nil {
			return 29 // EIO
		}
		if !mem.Write(bufPtr, buf) {
			return 21
		}
		return 0
	})

	// environ_sizes_get(count_ptr, buf_size_ptr) → errno
	b.export("environ_sizes_get", func(ctx context.Context, mod api.Module, countPtr, bufSizePtr uint32) uint32 {
		writeUint32(mod, countPtr, 0)
		writeUint32(mod, bufSizePtr, 0)
		return 0
	})

	// environ_get(environ_ptr, environ_buf_ptr) → errno
	b.export("environ_get", func(ctx context.Context, mod api.Module, environPtr, environBufPtr uint32) uint32 {
		return 0
	})

	// args_sizes_get(argc_ptr, argv_buf_size_ptr) → errno
	b.export("args_sizes_get", func(ctx context.Context, mod api.Module, argcPtr, argvBufSizePtr uint32) uint32 {
		writeUint32(mod, argcPtr, 0)
		writeUint32(mod, argvBufSizePtr, 0)
		return 0
	})

	// args_get(argv_ptr, argv_buf_ptr) → errno
	b.export("args_get", func(ctx context.Context, mod api.Module, argvPtr, argvBufPtr uint32) uint32 {
		return 0
	})

	// proc_exit(code) — 不真正退出进程
	b.export("proc_exit", func(ctx context.Context, mod api.Module, code uint32) {
		// no-op in sandbox
	})

	// fd_close, fd_seek, fd_read — stub 返回 EBADF
	b.export("fd_close", func(ctx context.Context, mod api.Module, fd uint32) uint32 {
		return 8
	})

	b.export("fd_seek", func(ctx context.Context, mod api.Module, fd uint32, offset uint64, whence uint32, newOffsetPtr uint32) uint32 {
		return 8
	})

	b.export("fd_read", func(ctx context.Context, mod api.Module, fd, iovsPtr, iovsLen, nreadPtr uint32) uint32 {
		return 8
	})

	// fd_fdstat_get — stub, 返回 regular file stat
	b.export("fd_fdstat_get", func(ctx context.Context, mod api.Module, fd, resultPtr uint32) uint32 {
		mem := mod.Memory()
		if mem == nil {
			return 8
		}
		// 写一个最小的 fdstat 结构(24字节全零, fs_filetype=4=REGULAR_FILE)
		stat := make([]byte, 24)
		stat[0] = 4 // fs_filetype = REGULAR_FILE
		mem.Write(resultPtr, stat)
		return 0
	})

	// fd_prestat_get — 返回 EBADF (无预打开目录)
	b.export("fd_prestat_get", func(ctx context.Context, mod api.Module, fd, resultPtr uint32) uint32 {
		return 8
	})

	// fd_prestat_dir_name — 返回 EBADF
	b.export("fd_prestat_dir_name", func(ctx context.Context, mod api.Module, fd, pathPtr, pathLen uint32) uint32 {
		return 8
	})

	// path_open — 返回 ENOENT
	b.export("path_open", func(ctx context.Context, mod api.Module, fd, dirflags, pathPtr, pathLen, oflags uint32, fsRightsBase, fsRightsInheriting uint64, fdflags, resultFdPtr uint32) uint32 {
		return 44 // ENOENT
	})

	// sched_yield — no-op
	b.export("sched_yield", func(ctx context.Context, mod api.Module) uint32 {
		return 0
	})

	// fd_advise — stub no-op
	b.export("fd_advise", func(ctx context.Context, mod api.Module, fd uint32, offset, length uint64, advice uint32) uint32 {
		return 0
	})

	// fd_allocate — stub EBADF
	b.export("fd_allocate", func(ctx context.Context, mod api.Module, fd uint32, offset, length uint64) uint32 {
		return 8
	})

	// fd_datasync — stub no-op
	b.export("fd_datasync", func(ctx context.Context, mod api.Module, fd uint32) uint32 {
		return 0
	})

	// fd_sync — stub no-op
	b.export("fd_sync", func(ctx context.Context, mod api.Module, fd uint32) uint32 {
		return 0
	})

	// fd_filestat_get — stub EBADF
	b.export("fd_filestat_get", func(ctx context.Context, mod api.Module, fd, resultPtr uint32) uint32 {
		return 8
	})

	// fd_filestat_set_size — stub EBADF
	b.export("fd_filestat_set_size", func(ctx context.Context, mod api.Module, fd uint32, size uint64) uint32 {
		return 8
	})

	// fd_filestat_set_times — stub EBADF
	b.export("fd_filestat_set_times", func(ctx context.Context, mod api.Module, fd uint32, atim, mtim uint64, fstFlags uint32) uint32 {
		return 8
	})

	// fd_pread — stub EBADF
	b.export("fd_pread", func(ctx context.Context, mod api.Module, fd, iovsPtr, iovsLen uint32, offset uint64, nreadPtr uint32) uint32 {
		return 8
	})

	// fd_pwrite — stub EBADF
	b.export("fd_pwrite", func(ctx context.Context, mod api.Module, fd, iovsPtr, iovsLen uint32, offset uint64, nwrittenPtr uint32) uint32 {
		return 8
	})

	// fd_readdir — stub EBADF
	b.export("fd_readdir", func(ctx context.Context, mod api.Module, fd, bufPtr, bufLen uint32, cookie uint64, resultPtr uint32) uint32 {
		return 8
	})

	// fd_renumber — stub EBADF
	b.export("fd_renumber", func(ctx context.Context, mod api.Module, fd, to uint32) uint32 {
		return 8
	})

	// fd_tell — stub EBADF
	b.export("fd_tell", func(ctx context.Context, mod api.Module, fd, resultPtr uint32) uint32 {
		return 8
	})

	// path_create_directory — stub ENOENT
	b.export("path_create_directory", func(ctx context.Context, mod api.Module, fd, pathPtr, pathLen uint32) uint32 {
		return 44
	})

	// path_filestat_get — stub ENOENT
	b.export("path_filestat_get", func(ctx context.Context, mod api.Module, fd, flags, pathPtr, pathLen, resultPtr uint32) uint32 {
		return 44
	})

	// path_filestat_set_times — stub ENOENT
	b.export("path_filestat_set_times", func(ctx context.Context, mod api.Module, fd, flags, pathPtr, pathLen uint32, atim, mtim uint64, fstFlags uint32) uint32 {
		return 44
	})

	// path_link — stub ENOENT
	b.export("path_link", func(ctx context.Context, mod api.Module, oldFd, oldFlags, oldPathPtr, oldPathLen, newFd, newPathPtr, newPathLen uint32) uint32 {
		return 44
	})

	// path_readlink — stub ENOENT
	b.export("path_readlink", func(ctx context.Context, mod api.Module, fd, pathPtr, pathLen, bufPtr, bufLen, resultPtr uint32) uint32 {
		return 44
	})

	// path_remove_directory — stub ENOENT
	b.export("path_remove_directory", func(ctx context.Context, mod api.Module, fd, pathPtr, pathLen uint32) uint32 {
		return 44
	})

	// path_rename — stub ENOENT
	b.export("path_rename", func(ctx context.Context, mod api.Module, fd, oldPathPtr, oldPathLen, newFd, newPathPtr, newPathLen uint32) uint32 {
		return 44
	})

	// path_symlink — stub ENOENT
	b.export("path_symlink", func(ctx context.Context, mod api.Module, oldPathPtr, oldPathLen, fd, newPathPtr, newPathLen uint32) uint32 {
		return 44
	})

	// path_unlink_file — stub ENOENT
	b.export("path_unlink_file", func(ctx context.Context, mod api.Module, fd, pathPtr, pathLen uint32) uint32 {
		return 44
	})

	// poll_oneoff — stub ENOSYS
	b.export("poll_oneoff", func(ctx context.Context, mod api.Module, inPtr, outPtr, nsubscriptions, resultPtr uint32) uint32 {
		writeUint32(mod, resultPtr, 0)
		return 52 // ENOSYS
	})

	// clock_res_get — stub
	b.export("clock_res_get", func(ctx context.Context, mod api.Module, clockID, resultPtr uint32) uint32 {
		mem := mod.Memory()
		if mem == nil {
			return 8
		}
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, 1) // 1ns resolution
		mem.Write(resultPtr, buf)
		return 0
	})

	// sock_accept — stub ENOSYS
	b.export("sock_accept", func(ctx context.Context, mod api.Module, fd, flags, resultFdPtr uint32) uint32 {
		return 52
	})

	// sock_recv — stub ENOSYS
	b.export("sock_recv", func(ctx context.Context, mod api.Module, fd, riDataPtr, riDataLen, riFlags, roDataLenPtr, roFlagsPtr uint32) uint32 {
		return 52
	})

	// sock_send — stub ENOSYS
	b.export("sock_send", func(ctx context.Context, mod api.Module, fd, siDataPtr, siDataLen, siFlags, soDataLenPtr uint32) uint32 {
		return 52
	})

	// sock_shutdown — stub ENOSYS
	b.export("sock_shutdown", func(ctx context.Context, mod api.Module, fd, how uint32) uint32 {
		return 52
	})
}
