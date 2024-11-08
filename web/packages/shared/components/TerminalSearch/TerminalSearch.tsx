import { useEffect } from 'react';
import { CSSTransition } from 'react-transition-group';
import styled from 'styled-components';
import { Flex, ButtonIcon, Box, P2, Input } from 'design';
import { Terminal } from '@xterm/xterm';
import { ISearchOptions } from '@xterm/addon-search';
import { ArrowDown, ArrowUp, Cross } from 'design/Icon';

import { TerminalSearchProps } from './useTerminalSearch';

export const TerminalSearch = ({
  onEscape,
  onKeyDown,
  searchNext,
  searchPrevious,
  onChange,
  xTerm,
  showSearch,
  setShowSearch,
  searchInputRef,
  searchResults,
  onSearchResultsChange,
}: TerminalSearchProps) => {
  useEffect(() => {
    if (!xTerm) {
      return;
    }
    xTerm.loadSearchAddon({ onDidChangeResults: onSearchResultsChange });
    const listener = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onEscape();
        e.preventDefault();
      }
      if ((e.metaKey || e.ctrlKey) && e.key === 'f') {
        setShowSearch(true);
        searchInputRef.current?.focus();
        e.preventDefault();
      }
    };

    document.addEventListener('keydown', listener);

    return () => {
      document.removeEventListener('keydown', listener);
    };
  }, [onEscape, setShowSearch, searchInputRef, onSearchResultsChange, xTerm]);

  return (
    <SearchTransitionStyles>
      <CSSTransition
        in={showSearch}
        timeout={150}
        classNames="search"
        unmountOnExit
      >
        <SearchInputBox alignItems="center" gap={3}>
          <Input
            ref={searchInputRef}
            size="small"
            autoFocus
            onChange={onChange}
            onKeyDown={onKeyDown}
          />
          <P2>{`${searchResults.resultCount === 0 ? 0 : searchResults.resultIndex + 1}/${searchResults.resultCount}`}</P2>
          <Divider />
          <Flex gap={1}>
            <SearchButtonIcon onClick={searchNext}>
              <ArrowDown size="small" />
            </SearchButtonIcon>
            <SearchButtonIcon onClick={searchPrevious}>
              <ArrowUp size="small" />
            </SearchButtonIcon>
            <SearchButtonIcon onClick={onEscape}>
              <Cross size="small" />
            </SearchButtonIcon>
          </Flex>
        </SearchInputBox>
      </CSSTransition>
    </SearchTransitionStyles>
  );
};

const SearchInputBox = styled(Flex)`
  position: absolute;
  padding: ${p => p.theme.space[2]}px;
  background-color: ${p => p.theme.colors.levels.surface};
  position: absolute;
  right: 8px;
  border-radius: ${props => props.theme.radii[2]}px;
  // width: 100%;
  top: 8px;
  z-index: 11;
`;

const SearchTransitionStyles = styled.div`
  /* Enter transition */
  .search-enter {
    transform: translateY(-20px);
  }
  .search-enter-active {
    transform: translateY(0);
    transition: all 150ms ease-in-out;
  }

  /* Exit transition */
  .search-exit {
    transform: translateY(0);
  }
  .search-exit-active {
    transform: translateY(-20px);
    transition: all 150ms ease-in-out;
  }
`;

const SearchButtonIcon = styled(ButtonIcon)`
  border-radius: ${props => props.theme.radii[2]}px;
`;

const Divider = styled(Box)`
  width: 2px;
  height: 24px;
  background-color: ${p => p.theme.colors.levels.popout};
`;

export interface TerminalWithSearch {
  term: Terminal;
  search(
    searchString: string,
    searchPrevious: boolean,
    searchOpts?: ISearchOptions
  ): void;
  clearSearch(): void;
  loadSearchAddon({
    onDidChangeResults,
  }: {
    onDidChangeResults: (resultIndex: number, resultCount: number) => void;
  }): void;
}
