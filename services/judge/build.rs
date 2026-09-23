fn main() -> Result<(), Box<dyn std::error::Error>> {
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    tonic_build::configure()
        .build_server(false)
        .compile_protos(
            &[
                "../../proto/soj/v1/common.proto",
                "../../proto/soj/judge/v1/job.proto",
                "../../proto/soj/judge/v1/control.proto",
            ],
            &["../../proto"],
        )?;
    println!("cargo:rerun-if-changed=../../proto/soj/v1/common.proto");
    println!("cargo:rerun-if-changed=../../proto/soj/judge/v1/job.proto");
    println!("cargo:rerun-if-changed=../../proto/soj/judge/v1/control.proto");
    Ok(())
}
