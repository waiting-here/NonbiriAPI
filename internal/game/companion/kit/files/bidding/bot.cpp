#include "arena.hpp"

// Hold the joker for a large prize. Spend the highest card on large pools.
Json choose_action(const Json& request) {
    const auto& options = request.at("space").at("examples");
    const auto pool = request.at("view").at("pool_points").get<int>();
    return pool >= 18 ? options.back() : options.front();
}
