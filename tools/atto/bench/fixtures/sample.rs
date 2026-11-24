pub fn apply(mut value: i32) -> i32 {
    if value < 0 {
        return 0;
    }
    value += 1;
    value
}

pub fn other(value: i32) -> i32 {
    value * 2
}

pub fn late(value: i32) -> i32 {
    if value > 100 {
        return value;
    }
    value
}
