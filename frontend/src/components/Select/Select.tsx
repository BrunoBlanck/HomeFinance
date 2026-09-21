import { Fragment, type Ref, type SelectHTMLAttributes, useId } from 'react'
import { type FieldDensity, FieldShell } from '../FieldShell/FieldShell'
import fieldStyles from '../FieldShell/FieldShell.module.css'
import { ChevronDownIcon } from '../icons/ChevronDownIcon'
import styles from './Select.module.css'

export type SelectOption = {
  value: string
  label: string
  /** Agrupa dentro de um `<optgroup>` — usado pela árvore de categorias. */
  group?: string | undefined
  disabled?: boolean | undefined
}

type SelectProps = {
  /** Obrigatório mesmo com `labelHidden`: o `<label for>` continua no DOM e
   *  associado ao `<select>`, então não há como pedir este componente e receber
   *  um controle sem nome acessível. Quando o nome precisa ser mais específico
   *  do que o rótulo visível ("Decisão para Pagamento de fatura, 07/08,
   *  R$ 2.859,82"), passe também `aria-label` — ele tem precedência sobre o
   *  `<label>` na hora de nomear. */
  label: string
  options: readonly SelectOption[]
  hint?: string | undefined
  error?: string | undefined
  /** Texto da opção vazia. Sem ele, o campo não tem estado "não escolhido" e o
   *  primeiro item vira uma escolha que o usuário nunca fez. */
  placeholder?: string | undefined
  /** `compact` para o select que mora numa célula de tabela ou numa barra de
   *  filtro: altura `--control-h-sm`, largura por conteúdo e slot de mensagem
   *  só quando há erro. */
  density?: FieldDensity | undefined
  /** Manda o rótulo para `.sr-only` — o cabeçalho da coluna já disse o que a
   *  célula é, e repetir o rótulo em 59 linhas seria ruído. */
  labelHidden?: boolean | undefined
  /** React 19: `ref` é prop normal — é por aqui que o react-hook-form registra. */
  ref?: Ref<HTMLSelectElement> | undefined
  /** Texto FORA do componente que também descreve o campo — ele é **somado** ao
   *  slot de mensagem, nunca o substitui.
   *
   *  Existe porque há descrição que não cabe dentro do `FieldShell`: a nota do
   *  editor de categoria de `/lancamentos` explica por que a categoria atual
   *  não está na lista, e 80 caracteres dentro do campo esticariam a coluna
   *  (docs/DESIGN.md (h) §2). Sem ela, quem chega pelo Tab ouve o rótulo e o
   *  placeholder, e nada explica o vazio. O `hint` continua sendo o caminho
   *  normal — este é para o texto que precisa morar fora. */
  'aria-describedby'?: string | undefined
} & Omit<
  SelectHTMLAttributes<HTMLSelectElement>,
  'className' | 'style' | 'children' | 'aria-describedby' | 'aria-invalid'
>

/** Seletor sobre o `<select>` NATIVO.
 *
 *  O design system manda usar o elemento nativo antes de reimplementar um
 *  padrão APG, e aqui o ganho é grande: no celular o `<select>` abre a roda do
 *  sistema, que é maior, mais rápida e mais familiar do que qualquer lista
 *  desenhada por nós; no teclado, digitar as primeiras letras já salta para a
 *  opção. Um combobox próprio só entra quando for preciso BUSCAR entre muitas
 *  opções — e aí ele vem pelo APG, na entrega que precisar. */
export function Select({
  label,
  options,
  hint,
  error,
  placeholder,
  density = 'form',
  labelHidden = false,
  id,
  required,
  ref,
  'aria-describedby': descritoPor,
  ...rest
}: SelectProps) {
  const rawId = useId()
  const campoId = id ?? `select-${rawId.replace(/[^a-zA-Z0-9]/g, '')}`
  const mensagemId = `${campoId}-message`
  // SOMA, não substituição: o slot de mensagem (hint/erro) vem sempre primeiro,
  // e o que o chamador mandou entra depois. Sem `aria-describedby` recebido o
  // resultado é exatamente o `mensagemId` de antes — a mudança é aditiva e
  // nenhum consumidor existente muda de DOM.
  const descricaoId = [mensagemId, descritoPor].filter(Boolean).join(' ')

  const baldes = agrupar(options)

  return (
    <FieldShell
      id={campoId}
      messageId={mensagemId}
      label={label}
      hint={hint}
      error={error}
      density={density}
      labelHidden={labelHidden}
    >
      <div className={styles.wrap} data-density={density} data-invalid={error ? true : undefined}>
        <select
          {...rest}
          ref={ref}
          id={campoId}
          required={required}
          className={fieldStyles.control}
          aria-describedby={descricaoId}
          aria-invalid={error ? true : undefined}
        >
          {placeholder ? <option value="">{placeholder}</option> : null}
          {baldes.map((balde) => {
            const opcoes = balde.opcoes.map((opcao) => (
              <option key={opcao.value} value={opcao.value} disabled={opcao.disabled}>
                {opcao.label}
              </option>
            ))
            return balde.grupo === undefined ? (
              <Fragment key={balde.chave}>{opcoes}</Fragment>
            ) : (
              <optgroup key={balde.chave} label={balde.grupo}>
                {opcoes}
              </optgroup>
            )
          })}
        </select>
        <span className={styles.chevron} aria-hidden="true">
          <ChevronDownIcon size={16} />
        </span>
      </div>
    </FieldShell>
  )
}

/** Um trecho CONSECUTIVO de opções com o mesmo `group` — `undefined` no trecho
 *  sem grupo nenhum.
 *
 *  `chave` é o `value` da primeira opção do trecho: único na lista, estável
 *  entre renders e — ao contrário do rótulo — não colide quando dois trechos se
 *  chamam igual. */
type Balde = { chave: string; grupo: string | undefined; opcoes: SelectOption[] }

/** Agrupa por TRECHO CONSECUTIVO, não por nome.
 *
 *  A ordem de saída é exatamente a de entrada: a árvore de categorias já chega
 *  ordenada do servidor, e reordenar aqui inventaria uma ordem própria. Um
 *  balde novo abre toda vez que `group` muda em relação à opção **anterior**,
 *  inclusive de indefinido para definido e vice-versa.
 *
 *  A versão anterior montava um `Map` por rótulo e despejava as opções SEM
 *  grupo antes de todos os `<optgroup>` — o que virou mentira visível com as
 *  quatro naturezas (E7 (l) de docs/DESIGN.md): `opcoesDeCategoria` emite grupo
 *  sem subcategoria como opção solta, então a casa com o grupo "Investimentos"
 *  da semente via "Investimentos" no TOPO do seletor de despesa, acima de
 *  "Alimentação › Mercado", sem cabeçalho e sem nada dizendo que aquilo é
 *  investimento.
 *
 *  Limite residual, declarado: dois grupos de mesmo nome **adjacentes** ainda
 *  se fundem num balde só. Hoje é inalcançável — `NameTaken` não olha a
 *  natureza, então dois grupos ativos de mesmo nome não coexistem, e os dois
 *  chamadores pedem a árvore sem arquivadas. No dia em que um dos dois mudar, o
 *  balde precisa de chave própria vinda do chamador (`groupKey`), não do
 *  rótulo. */
function agrupar(options: readonly SelectOption[]): Balde[] {
  const baldes: Balde[] = []

  for (const opcao of options) {
    const grupo = opcao.group || undefined
    const ultimo = baldes[baldes.length - 1]
    if (ultimo && ultimo.grupo === grupo) {
      ultimo.opcoes.push(opcao)
      continue
    }
    baldes.push({ chave: opcao.value, grupo, opcoes: [opcao] })
  }
  return baldes
}
