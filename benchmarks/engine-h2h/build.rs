fn main() {
    let path = std::path::PathBuf::from(std::env::var("CARGO_MANIFEST_DIR").unwrap()).join("build");
    println!("cargo:rustc-link-search=native={}", path.display());
    println!("cargo:rustc-link-lib=static=golsm");
    for lib in ["pthread", "dl", "m", "resolv"] {
        println!("cargo:rustc-link-lib={lib}");
    }
    println!("cargo:rerun-if-changed={}/libgolsm.a", path.display());
}
