#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p build/kiban build/golsm
if [ ! -d build/upstream.git ]; then
 git clone --bare --filter=blob:none https://github.com/aetosdios27/kiban.git build/upstream.git
fi
git --git-dir=build/upstream.git archive 361305a8b63fef309e301e8106c254e60a70ac84 src examples benches tests Cargo.toml Cargo.lock LICENSE README.md | tar -x -C build/kiban
repo_root=$(cd ../.. && pwd)
for file in db.go memtable.go codec.go bloom.go mmap.go cache.go manifest.go compaction.go range.go metrics.go; do
 git -C "$repo_root" show "b9a1c07a85be14eabdcba6dc129bfeaf7a59a82a:$file" > "build/golsm/$file"
done
cp go/adapter.go build/golsm/
printf 'module golsmenginebench\n\ngo 1.25.0\n' > build/golsm/go.mod
(cd build/golsm && CGO_ENABLED=1 go build -tags enginebench -buildmode=c-archive -o ../libgolsm.a .)
if [ -f Cargo.lock ]; then cargo build --release --locked; else cargo build --release; fi
sha256sum target/release/golsm-engine-h2h build/libgolsm.a > build/binary-hashes.txt
