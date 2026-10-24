pub use crate::api::{Backend, Result};
pub use crate::embedded::Embedded;
use crate::{
    Mode,
    model::{Memory, key},
};
use heed3::{Database, Env, EnvFlags, EnvOpenOptions, FlagSetMode, types::Bytes};
use std::path::Path;

pub fn configure(env: &Env, mode: Mode) -> Result<serde_json::Value> {
    let relaxed = EnvFlags::NO_SYNC | EnvFlags::NO_META_SYNC;
    if mode == Mode::Fast {
        // This process owns the environment; no transaction or worker is active.
        unsafe {
            env.set_flags(relaxed, FlagSetMode::Enable)?;
        }
    }
    let actual = env.flags()?.ok_or("unknown LMDB environment flag")?;
    let expected = if mode == Mode::Fast {
        relaxed
    } else {
        EnvFlags::empty()
    };
    if actual.intersection(relaxed) != expected {
        return Err("LMDB durability flags do not match requested mode".into());
    }
    Ok(
        serde_json::json!({"lmdb_flags": format!("{actual:?}"), "lmdb_flags_bits": actual.bits(),
        "map_size_bytes": env.info().map_size, "heed3": "0.22.0"}),
    )
}

pub struct Lmdb {
    env: Env,
    records: Database<Bytes, Bytes>,
}

impl Lmdb {
    pub fn open(path: &Path, mode: Mode) -> Result<(Self, serde_json::Value)> {
        std::fs::create_dir_all(path)?;
        // The path belongs exclusively to this trial/process; no external LMDB user.
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(10 * 1024 * 1024 * 1024)
                .max_dbs(200)
                .max_readers(200)
                .open(path)?
        };
        let mut txn = env.write_txn()?;
        let records = env.create_database(&mut txn, Some("memories"))?;
        txn.commit()?;
        let details = configure(&env, mode)?;
        Ok((Self { env, records }, details))
    }
}

impl Backend for Lmdb {
    fn bulk_load(&mut self, n: u64, seed: u64) -> Result<()> {
        let mut txn = self.env.write_txn()?;
        for id in 0..n {
            let memory = Memory::new(id, 0, seed);
            self.records.put(
                &mut txn,
                memory.key().as_bytes(),
                &serde_json::to_vec(&memory)?,
            )?;
            if (id + 1) % 10000 == 0 {
                eprintln!("lmdb: bulk preload {}/{}", id + 1, n);
            }
        }
        txn.commit()?;
        Ok(())
    }
    fn put(&mut self, memory: &Memory, _insert: bool) -> Result<()> {
        let key = memory.key();
        let bytes = serde_json::to_vec(memory)?;
        let mut txn = self.env.write_txn()?;
        self.records.put(&mut txn, key.as_bytes(), &bytes)?;
        txn.commit()?;
        Ok(())
    }

    fn get(&mut self, id: u64) -> Result<Memory> {
        let txn = self.env.read_txn()?;
        let bytes = self
            .records
            .get(&txn, key(id).as_bytes())?
            .ok_or("missing LMDB memory")?;
        let result = serde_json::from_slice(bytes)?;
        txn.commit()?;
        Ok(result)
    }

    fn recent(&mut self, agent: u32, k: usize) -> Result<Vec<Memory>> {
        let txn = self.env.read_txn()?;
        let prefix = format!("agent/{agent:04}/memory/");
        let result = self
            .records
            .prefix_iter(&txn, prefix.as_bytes())?
            .take(k)
            .map(|entry| {
                let (_, bytes) = entry?;
                Ok(serde_json::from_slice(bytes)?)
            })
            .collect::<Result<Vec<_>>>()?;
        txn.commit()?;
        Ok(result)
    }

    fn finish(&mut self) -> Result<()> {
        self.env.force_sync()?;
        Ok(())
    }
}

use std::ffi::{CStr, CString, c_char, c_int, c_void};
unsafe extern "C" {
    fn GolsmOpen(
        path: *const c_char,
        durable: c_int,
        threshold: c_int,
        error: *mut *mut c_char,
    ) -> u64;
    fn GolsmClose(handle: u64, error: *mut *mut c_char) -> c_int;
    fn GolsmPut(
        handle: u64,
        key: *const c_void,
        key_len: i64,
        value: *const c_void,
        value_len: i64,
        error: *mut *mut c_char,
    ) -> c_int;
    fn GolsmGet(
        handle: u64,
        key: *const c_void,
        key_len: i64,
        out: *mut *mut c_void,
        out_len: *mut i64,
        error: *mut *mut c_char,
    ) -> c_int;
    fn GolsmScan(
        handle: u64,
        start: *const c_char,
        end: *const c_char,
        limit: c_int,
        out: *mut *mut c_void,
        out_len: *mut i64,
        error: *mut *mut c_char,
    ) -> c_int;
    fn GolsmFinish(handle: u64, error: *mut *mut c_char) -> c_int;
    fn free(ptr: *mut c_void);
    fn GolsmState(out: *mut *mut c_void, out_len: *mut i64);
    fn GolsmNoop() -> c_int;
    fn GolsmEcho(data: *const c_void, len: i64) -> *mut c_void;
}
fn go_error(error: *mut c_char) -> Box<dyn std::error::Error> {
    if error.is_null() {
        return "GoLSM operation failed".into();
    }
    let text = unsafe { CStr::from_ptr(error) }
        .to_string_lossy()
        .into_owned();
    unsafe { free(error.cast()) };
    text.into()
}
struct GoBuffer {
    ptr: *mut c_void,
    len: i64,
}
impl GoBuffer {
    fn bytes(&self) -> &[u8] {
        if self.len == 0 {
            return &[];
        };
        unsafe { std::slice::from_raw_parts(self.ptr.cast(), self.len as usize) }
    }
}
impl Drop for GoBuffer {
    fn drop(&mut self) {
        unsafe { free(self.ptr) }
    }
}
pub struct GoLSM {
    handle: u64,
    path: CString,
}
impl GoLSM {
    pub fn open(path: &Path, mode: Mode) -> Result<(Self, serde_json::Value)> {
        let path = CString::new(path.as_os_str().as_encoded_bytes())?;
        let mut error = std::ptr::null_mut();
        let handle = unsafe {
            GolsmOpen(
                path.as_ptr(),
                (mode == Mode::Durable) as c_int,
                0,
                &mut error,
            )
        };
        if handle == 0 {
            return Err(go_error(error));
        }
        Ok((
            Self { handle, path },
            serde_json::json!({"per_write_sync":mode==Mode::Durable,"bridge":"Go c-archive; Rust -> C -> Go; calls, copies and returned-buffer free included","flush_threshold":1000,"cache_bytes":16777216,"compaction_interval_seconds":30,"compaction_threshold":4,"engine_commit":"b9a1c07a85be14eabdcba6dc129bfeaf7a59a82a"}),
        ))
    }
    fn close(&mut self) -> Result<()> {
        if self.handle != 0 {
            let mut error = std::ptr::null_mut();
            let status = unsafe { GolsmClose(self.handle, &mut error) };
            self.handle = 0;
            if status != 0 {
                return Err(go_error(error));
            }
        };
        Ok(())
    }
}
impl Drop for GoLSM {
    fn drop(&mut self) {
        if let Err(error) = self.close() {
            eprintln!("INVALID: GoLSM close: {error}");
            std::process::abort()
        }
    }
}
impl Backend for GoLSM {
    fn bulk_load(&mut self, n: u64, seed: u64) -> Result<()> {
        // Match the upstream relaxed, coalesced bulk preload. The foreground
        // process reopens with default options before any timed calls.
        self.close()?;
        let mut error = std::ptr::null_mut();
        self.handle = unsafe { GolsmOpen(self.path.as_ptr(), 0, (n + 1).try_into()?, &mut error) };
        if self.handle == 0 {
            return Err(go_error(error));
        }
        for id in 0..n {
            self.put(&Memory::new(id, 0, seed), true)?;
            if (id + 1) % 10000 == 0 {
                eprintln!("golsm: bulk preload {}/{}", id + 1, n)
            }
        }
        Ok(())
    }
    fn put(&mut self, memory: &Memory, _insert: bool) -> Result<()> {
        let key = memory.key();
        let bytes = serde_json::to_vec(memory)?;
        let mut error = std::ptr::null_mut();
        let status = unsafe {
            GolsmPut(
                self.handle,
                key.as_ptr().cast(),
                key.len() as i64,
                bytes.as_ptr().cast(),
                bytes.len() as i64,
                &mut error,
            )
        };
        if status != 0 {
            return Err(go_error(error));
        };
        Ok(())
    }
    fn get(&mut self, id: u64) -> Result<Memory> {
        let key = key(id);
        let mut error = std::ptr::null_mut();
        let mut value = GoBuffer {
            ptr: std::ptr::null_mut(),
            len: 0,
        };
        let status = unsafe {
            GolsmGet(
                self.handle,
                key.as_ptr().cast(),
                key.len() as i64,
                &mut value.ptr,
                &mut value.len,
                &mut error,
            )
        };
        if status == 1 {
            return Err("missing GoLSM memory".into());
        };
        if status != 0 {
            return Err(go_error(error));
        };
        Ok(serde_json::from_slice(value.bytes())?)
    }
    fn recent(&mut self, agent: u32, k: usize) -> Result<Vec<Memory>> {
        let start = CString::new(format!("agent/{agent:04}/memory/"))?;
        let end = CString::new(format!("agent/{agent:04}/memory0"))?;
        let mut error = std::ptr::null_mut();
        let mut value = GoBuffer {
            ptr: std::ptr::null_mut(),
            len: 0,
        };
        let status = unsafe {
            GolsmScan(
                self.handle,
                start.as_ptr(),
                end.as_ptr(),
                k as c_int,
                &mut value.ptr,
                &mut value.len,
                &mut error,
            )
        };
        if status != 0 {
            return Err(go_error(error));
        };
        let mut b = value.bytes();
        let mut rows = Vec::new();
        while !b.is_empty() {
            if b.len() < 4 {
                return Err("short GoLSM scan header".into());
            };
            let n = u32::from_le_bytes(b[..4].try_into()?) as usize;
            b = &b[4..];
            if n > b.len() {
                return Err("short GoLSM scan value".into());
            };
            rows.push(serde_json::from_slice(&b[..n])?);
            b = &b[n..]
        }
        Ok(rows)
    }
    fn finish(&mut self) -> Result<()> {
        let mut error = std::ptr::null_mut();
        let status = unsafe { GolsmFinish(self.handle, &mut error) };
        if status != 0 {
            return Err(go_error(error));
        };
        Ok(())
    }
}
pub fn calibrate() {
    use crate::metrics::Histogram;
    use std::time::Instant;
    let payload = serde_json::to_vec(&Memory::new(0, 0, 20261016)).unwrap();
    let mut noop = Histogram::default();
    let mut echo = Histogram::default();
    for _ in 0..1000 {
        unsafe {
            std::hint::black_box(GolsmNoop());
        }
    }
    for _ in 0..100000 {
        let start = Instant::now();
        unsafe {
            std::hint::black_box(GolsmNoop());
        };
        noop.record(start.elapsed().as_nanos() as u64);
        let start = Instant::now();
        let out = unsafe { GolsmEcho(payload.as_ptr().cast(), payload.len() as i64) };
        assert!(!out.is_null());
        unsafe {
            std::hint::black_box(*(out.cast::<u8>()));
            free(out)
        };
        echo.record(start.elapsed().as_nanos() as u64)
    }
    println!(
        "{}",
        serde_json::json!({"calls":100000,"payload_bytes":payload.len(),"noop_p50_ns":noop.percentile(50,100),"noop_p99_ns":noop.percentile(99,100),"echo_copy_free_p50_ns":echo.percentile(50,100),"echo_copy_free_p99_ns":echo.percentile(99,100),"note":"Excluded diagnostic. No latency subtraction; actual GoLSM timings include bridging."})
    );
}

pub fn state(backend: &str) -> serde_json::Value {
    if backend != "golsm" {
        return serde_json::Value::Null;
    };
    let mut buf = GoBuffer {
        ptr: std::ptr::null_mut(),
        len: 0,
    };
    unsafe { GolsmState(&mut buf.ptr, &mut buf.len) };
    serde_json::from_slice(buf.bytes()).expect("valid GoLSM metrics JSON")
}
