export type CompanionGame = 'bidding' | 'likes' | 'gwent';

export const uploadsEnabled = false;
export const gameUploadsEnabled: Readonly<Record<CompanionGame, boolean>> = {
  bidding: false,
  likes: false,
  gwent: false,
};

export function companionUploadsAvailable(game: CompanionGame, advertised: boolean) {
  return uploadsEnabled && gameUploadsEnabled[game] && advertised;
}

export type ProgramVersion = {
  version: number;
  status: 'ready' | 'failed';
  diagnostic?: string;
  created_at: number;
};

export type CompanionPrograms = {
  enabled: boolean;
  upload_available: boolean;
  upload_global_enabled?: boolean;
  upload_game_enabled?: boolean;
  official: { name: string; version: number; difficulties: number[]; versions?: ProgramVersion[] };
  programs: { name: string; version: number | null; versions: ProgramVersion[] }[];
};

export function companionPath(game: CompanionGame, admin = false) {
  return (admin ? '/admin' : '') + '/api/games/' + game + '/companion';
}
