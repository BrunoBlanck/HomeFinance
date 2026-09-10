import styles from './LedgerPreview.module.css'

const COLUMNS = ['Data', 'Descrição', 'Categoria', 'Valor']
const EMPTY_ROWS = [0, 1, 2, 3, 4]

/** A estrutura real do que vem, desenhada com os próprios tokens. Usa borda em
 *  vez de bloco preenchido justamente para não se confundir com skeleton: não
 *  há nada carregando aqui, há uma folha em branco. */
export function LedgerPreview() {
  return (
    <div className={styles.sheet}>
      <div className={styles.head}>
        {COLUMNS.map((column) => (
          <span key={column}>{column}</span>
        ))}
      </div>
      {EMPTY_ROWS.map((row) => (
        <div className={styles.row} key={row}>
          <span />
          <span />
          <span />
          <span className={styles.amount}>—</span>
        </div>
      ))}
    </div>
  )
}
