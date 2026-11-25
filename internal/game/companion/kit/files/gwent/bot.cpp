#include "arena.hpp"

Json choose_action(const Json& request) {
    const auto& options = request.at("space").at("examples");
    for (const auto& action : options) {
        if (action.at("kind") == "play") return action;
    }
    return options.front();
}
