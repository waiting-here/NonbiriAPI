#include "arena.hpp"
#include "bot.cpp"
#include <iostream>

#ifndef ARENA_CUSTOM_SETUP
Json choose_setup(const Json& request) {
    return request.at("space").at("examples").at(0);
}
#endif

int main() {
    Json request;
    std::cin >> request;
    const Json action = request.at("phase") == "setup" ? choose_setup(request) : choose_action(request);
    Json reply = {{"protocol", request.at("protocol")}, {"decision_id", request.at("decision_id")}, {"action", action}};
    if (arena_memory_changed) reply["memory"] = arena_next_memory;
    std::cout << reply.dump() << '\n';
}
