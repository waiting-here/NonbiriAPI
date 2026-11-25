#include "arena.hpp"

// The reference plans are legal. You may also construct a plan with purchases
// and additional skills. See the sample and the mode's catalog for their IDs.
Json choose_action(const Json& request) {
    const auto& options = request.at("space").at("examples");
    for (const auto& action : options) {
        if (!action.at("plan").at("main").is_null()) return action;
    }
    return options.front();
}
