use std::collections::HashMap;

#[derive(Debug)]
struct Counter {
    counts: HashMap<String, u64>,
}

impl Counter {
    fn add(&mut self, key: &str) {
        *self.counts.entry(key.to_string()).or_insert(0) += 1;
    }

    fn get(&self, key: &str) -> u64 {
        *self.counts.get(key).unwrap_or(&0)
    }
}

fn main() {
    let mut c = Counter { counts: HashMap::new() };
    c.add("a");
    println!("{}", c.get("a"));
}
