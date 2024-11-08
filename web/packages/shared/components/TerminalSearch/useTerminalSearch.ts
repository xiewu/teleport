import React, { useCallback, useRef, useState } from 'react';
import { useTheme } from 'styled-components';

import { TerminalWithSearch } from './TerminalSearch';

export const useTerminalSearch = (
  xTerm: TerminalWithSearch
): TerminalSearchProps => {
  const theme = useTheme();
  const searchInputRef = useRef<HTMLInputElement>();
  const [searchValue, setSearchValue] = useState('');
  const [showSearch, setShowSearch] = useState(false);
  const [searchResults, setSearchResults] = useState<{
    resultIndex: number;
    resultCount: number;
  }>({ resultIndex: 0, resultCount: 0 });

  function search(value: string, prev?: boolean) {
    if (!xTerm) {
      return;
    }
    const match = theme.colors.terminal.brightYellow;
    const activeMatch = theme.colors.terminal.yellow;
    setSearchValue(value);

    xTerm.search(value, prev, {
      decorations: {
        matchOverviewRuler: match,
        activeMatchColorOverviewRuler: activeMatch,
        matchBackground: match,
        activeMatchBackground: activeMatch,
      },
    });
  }

  const onChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    search(e.target.value);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      search(e.currentTarget.value);
    }
  };

  function searchNext() {
    search(searchValue);
  }

  function searchPrevious() {
    search(searchValue, true /* search for previous result */);
  }

  const clear = useCallback(() => {
    xTerm.clearSearch();
    setSearchResults({ resultCount: 0, resultIndex: 0 });
  }, [xTerm]);

  const onEscape = useCallback(() => {
    setShowSearch(false);
    clear();
    xTerm.term.focus();
  }, [xTerm, clear]);

  const onSearchResultsChange = useCallback(
    (resultIndex: number, resultCount: number) => {
      setSearchResults({ resultIndex, resultCount });
    },
    []
  );

  return {
    xTerm,
    onChange,
    searchNext,
    searchPrevious,
    onKeyDown,
    searchInputRef,
    onEscape,
    setShowSearch,
    searchResults,
    showSearch,
    onSearchResultsChange,
  };
};

export type TerminalSearchProps = {
  xTerm: TerminalWithSearch;
  onSearchResultsChange(resultIndex: number, resultCount: number): void;
  searchNext(): void;
  searchPrevious(): void;
  searchResults: { resultIndex: number; resultCount: number };
  onKeyDown(e: React.KeyboardEvent<HTMLInputElement>): void;
  onChange(e: React.ChangeEvent<HTMLInputElement>): void;
  searchInputRef: React.MutableRefObject<HTMLInputElement>;
  showSearch: boolean;
  onEscape(): void;
  setShowSearch(show: boolean): void;
};
