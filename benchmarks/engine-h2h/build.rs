fn main() {
    let path = std::path::PathBuf::from(std::env::var("CARGO_MANIFEST_DIR").unwrap()).join("build");
    let revision_path = path.join("golsm-revision.txt");
    let revision = std::fs::read_to_string(&revision_path).expect("run build.sh first");
    println!("cargo:rustc-env=GOLSM_ENGINE_REVISION={}", revision.trim());
    println!("cargo:rerun-if-changed={}", revision_path.display());
    println!("cargo:rustc-link-search=native={}", path.display());
    println!("cargo:rustc-link-lib=static=golsm");
    for lib in ["pthread", "dl", "m", "resolv"] {
        println!("cargo:rustc-link-lib={lib}");
    }
    println!("cargo:rerun-if-changed={}/libgolsm.a", path.display());
}
