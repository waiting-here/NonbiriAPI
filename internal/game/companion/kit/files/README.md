# Companion program kit

Edit `bot.cpp` and upload that single file to the corresponding game's companion page. A C++17 compiler can run the included sample locally:

```sh
c++ -std=c++17 -Isdk -I. sdk/main.cpp -o bot
./bot < sample.json
```

The program reads one JSON decision and writes one JSON reply. The SDK copies the decision ID and protocol into the reply. Return one of the supplied examples or a valid structured plan or setup. Define `ARENA_CUSTOM_SETUP` and implement `Json choose_setup(const Json&)` to choose a deck or loadout. Preserve every constraint. Use `set_memory(string)` to return up to 8192 UTF-8 bytes for the next decision in this match.

`load_catalog()` reads the active rule catalog from the sandbox. For local practice it reads `catalog.json`. The kit includes the standard catalog under that name. Copy the quick catalog over it for a quick-mode sample. The service supplies only the current seat's observation. Each decision has 20 seconds of execution time.

When the request contains a history source, use `history_query(operation, args)` with its snapshot. Operations are `list`, `read`, `view_at` and `aggregate`. Snapshot, record, cursor, step and seat values refer to the current game. The local sample has no history. A private program receives only its author's permitted personal history.
