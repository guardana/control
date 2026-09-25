// Assembly in a guarded package, which reaches the clock or the kernel with no
// import at all. It holds no symbol, so it assembles on every architecture and
// go list reports it wherever the gate runs.
