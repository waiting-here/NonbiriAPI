#pragma once
#include "json.hpp"
#include <fstream>
#include <string>

using Json = nlohmann::json;
inline Json load_catalog() {
#ifdef __wasi__
    std::ifstream stream("/rules/catalog.json");
#else
    std::ifstream stream("catalog.json");
#endif
    Json catalog;
    stream >> catalog;
    return catalog;
}
inline std::string arena_next_memory;
inline bool arena_memory_changed = false;
inline void set_memory(const std::string& value) {
    arena_next_memory = value;
    arena_memory_changed = true;
}

#ifdef __wasi__
extern "C" __attribute__((import_module("nonbiri_history"), import_name("query")))
int history_size(const char*, unsigned, const char*, unsigned);
extern "C" __attribute__((import_module("nonbiri_history"), import_name("read")))
int history_read(char*, unsigned);
#endif

inline Json history_query(const std::string& operation, const Json& args) {
#ifdef __wasi__
    const std::string payload = args.dump();
    const int length = history_size(operation.data(), operation.size(), payload.data(), payload.size());
    if (length <= 0) return nullptr;
    std::string result(length, '\0');
    if (history_read(result.data(), result.size()) != length) return nullptr;
    return Json::parse(result);
#else
    (void)operation;
    (void)args;
    return nullptr;
#endif
}
