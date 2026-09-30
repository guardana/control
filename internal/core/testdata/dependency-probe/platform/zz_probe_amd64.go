package ioprobe

// DependencyProbeAMD64 is compiled for amd64 only, chosen by the file name with
// no constraint line. A machine of another architecture leaves it out of its
// own listing; an amd64 machine sees it left out only by the foreign listing.
func DependencyProbeAMD64() {}
