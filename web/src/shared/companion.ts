export type CompanionGame = 'bidding' | 'likes' | 'gwent';

export type ProgramVersion = {
  version: number;
  status: 'ready' | 'failed';
  diagnostic?: string;
  created_at: number;
};

export type CompanionPrograms = {
  enabled: boolean;
  upload_available: boolean;
  official: { name: string; version: number; difficulties: number[]; versions?: ProgramVersion[] };
  programs: { name: string; version: number | null; versions: ProgramVersion[] }[];
};

export function companionPath(game: CompanionGame, admin = false) {
  return (admin ? '/admin' : '') + '/api/games/' + game + '/companion';
}
