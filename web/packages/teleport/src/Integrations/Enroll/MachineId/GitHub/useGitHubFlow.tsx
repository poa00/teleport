import React, { useState, useContext } from 'react';
import { Option } from 'shared/components/Select';

import useAttempt, { Attempt } from 'shared/hooks/useAttemptNext';

import { BotConfig } from 'teleport/services/bot/types';
import { GitHubRepoRule, RefType } from 'teleport/services/joinToken';
import useTeleport from 'teleport/useTeleport';

type GitHubFlowContext = {
  attempt: Attempt;
  botConfig: BotConfig;
  setBotConfig: React.Dispatch<React.SetStateAction<BotConfig>>;
  repoRules: Rule[];
  setRepoRules: React.Dispatch<React.SetStateAction<Rule[]>>;
  addEmptyRepoRule: () => void;
  tokenName: string;
  createBot: () => Promise<boolean>;
  resetAttempt: () => void;
};

const gitHubFlowContext = React.createContext<GitHubFlowContext>(null);

export function GitHubFlowProvider({ children }: React.PropsWithChildren) {
  const { botService, resourceService, joinTokenService } = useTeleport();
  const { attempt, run } = useAttempt();
  const [botConfig, setBotConfig] = useState<BotConfig>({
    labels: [{ name: '*', value: '*' }],
    login: '',
    botName: '',
    roles: [],
  });
  const [repoRules, setRepoRules] = useState<Rule[]>([defaultRule]);
  const [tokenName, setTokenName] = useState('');

  function addEmptyRepoRule() {
    setRepoRules([...repoRules, defaultRule]);
  }

  function resetAttempt() {
    if (attempt.status !== 'processing') {
      attempt.status = '';
    }
  }

  function createBot(): Promise<boolean> {
    // TODO validations

    return run(() =>
      resourceService.createRole(``).then(() =>
        joinTokenService
          .fetchJoinToken({
            roles: ['Bot'],
            botName: botConfig.botName,
            gitHub: {
              allow: repoRules.map((r): GitHubRepoRule => {
                const { owner, repository } = parseRepoAddress(r.repoAddress);
                return {
                  repository: repository,
                  repository_owner: owner,
                  actor: r.actor,
                  environment: r.environment,
                  ref: r.ref,
                  ref_type: r.refType.value || null,
                  workflow: r.workflowName,
                  // TODO sub
                };
              }),
            },
            method: 'github',
          })
          .then(token => {
            setTokenName(token.id);
            return botService.createBot(botConfig);
          })
      )
    );
  }

  const value: GitHubFlowContext = {
    attempt,
    botConfig,
    setBotConfig,
    repoRules,
    setRepoRules,
    addEmptyRepoRule,
    tokenName,
    createBot,
    resetAttempt,
  };

  return (
    <gitHubFlowContext.Provider value={value}>
      {children}
    </gitHubFlowContext.Provider>
  );
}

export function useGitHubFlow(): GitHubFlowContext {
  return useContext(gitHubFlowContext);
}

export type RefTypeOption = Option<RefType | ''>;

export type Rule = {
  workflowName: string;
  environment: string;
  ref: string;
  refType: RefTypeOption;
  repoAddress: string;
  actor: string;
};

export const defaultRule: Rule = {
  workflowName: '',
  environment: '',
  ref: '',
  refType: { label: 'Branch', value: 'branch' },
  repoAddress: '',
  actor: '',
};

/**
 * Parses the GitHub repository URL and returns the repository name and
 * its owner's name. Throws errors if parsing the URL fails or
 * the URL doesn't contains the expected format.
 * @param repoAddr repository address (with or without protocl)
 * @returns owner and repository name
 */
export function parseRepoAddress(repoAddr: string): {
  owner: string;
  repository: string;
} {
  // add protocol if it is missing
  if (!repoAddr.startsWith('http://') && !repoAddr.startsWith('https://')) {
    repoAddr = `https://${repoAddr}`;
  }

  let url;
  try {
    url = new URL(repoAddr);
  } catch (e) {
    throw new Error('Must be a valid URL');
  }

  // TODO:
  // - account for enterprise github hosts
  //   - some enterprise github host may have slugs?
  // - write tests for this method
  const paths = url.pathname.split('/');
  // expected length is 3, since pathname starts with a /, so paths[0] should be empty
  if (paths.length < 3) {
    throw new Error(
      'URL expected to be in the format https://<host>/<owner>/<repository>'
    );
  }

  const owner = paths[1];
  const repository = paths[2];
  if (owner.trim() === '' || repository.trim() == '') {
    throw new Error(
      'URL expected to be in the format https://<host>/<owner>/<repository>'
    );
  }

  return {
    owner,
    repository,
  };
}
