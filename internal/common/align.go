package common

func AlignUp(x, a uint64) uint64 {
    if a == 0 { return x }
    r := x % a
    if r == 0 { return x }
    return x + (a - r)
}
